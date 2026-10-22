// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ebpfcommon // import "go.opentelemetry.io/obi/pkg/ebpf/common/http"

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"go.opentelemetry.io/obi/pkg/appolly/app/request"
)

const (
	// Lambda Invoke: POST /2015-03-31/functions/{FunctionName}/invocations[?Qualifier=...]
	lambdaInvokePathPrefix = "/2015-03-31/functions/"
	lambdaInvokePathSuffix = "/invocations"
	// shortest accepted remainder of a truncated Invoke path: no other
	// POST /2015-03-31/functions/<name>/... API starts with it
	lambdaTruncatedSuffixMin = "/i"

	lambdaFunctionErrorHeader = "x-amz-function-error"
	lambdaExecutedVersion     = "x-amz-executed-version"
	lambdaInvocationType      = "x-amz-invocation-type"
	amznErrorTypeHeader       = "x-amzn-errortype"
)

// awsLambdaErrorPayload is the body Lambda returns for a function error
// (x-amz-function-error: Unhandled|Handled), in every managed runtime.
type awsLambdaErrorPayload struct {
	ErrorMessage string `json:"errorMessage"`
	ErrorType    string `json:"errorType"`
}

// AWSLambdaSpan recognizes an AWS Lambda Invoke call (any SDK, any language:
// the operation is fully identified by method + path) and enriches the HTTP
// client span with the invoked function. Other Lambda APIs are not handled.
func AWSLambdaSpan(baseSpan *request.Span, req *http.Request, resp *http.Response) (request.Span, bool) {
	lambda, err := parseAWSLambda(req, resp, request.HostFromSchemeHost(baseSpan))
	if err != nil {
		return *baseSpan, false
	}

	// https://opentelemetry.io/docs/specs/semconv/faas/faas-spans/#outgoing-invocations
	baseSpan.SubType = request.HTTPSubtypeAWSLambda
	baseSpan.AWS = &request.AWS{
		Lambda: lambda,
	}

	return *baseSpan, true
}

// parseAWSLambda parses an Invoke call. spanHost is the host the client
// instrumentation reported (Go: Request.Host / URL.Host); it stands in for the
// Host of requests rebuilt from HTTP/2 frames, which carry the peer IP instead.
func parseAWSLambda(req *http.Request, resp *http.Response, spanHost string) (request.AWSLambda, error) {
	lambda := request.AWSLambda{}

	if req.Method != http.MethodPost || req.URL == nil {
		return lambda, errors.New("not a Lambda Invoke request")
	}
	functionID, ok := lambdaInvokeFunctionID(req.URL)
	if !ok {
		return lambda, errors.New("not a Lambda Invoke request")
	}

	var err error
	lambda.Meta, err = parseAWSMeta(req, resp)
	if err != nil {
		return lambda, err
	}
	if spanHost != "" && !awsRegionURLRgx.MatchString(req.Host) {
		lambda.Meta.Region = parseAWSRegion(&http.Request{Host: spanHost})
	}

	lambda.Operation = "Invoke"
	lambda.FunctionName, lambda.Qualifier, lambda.FunctionRegion = parseLambdaFunctionID(functionID)
	if lambda.FunctionName == "" {
		return lambda, errors.New("missing Lambda function name")
	}
	if q := req.URL.Query().Get("Qualifier"); q != "" {
		lambda.Qualifier = q
	}
	if lambda.FunctionRegion == "" {
		lambda.FunctionRegion = lambda.Meta.Region
	}
	lambda.FunctionARN = lambdaFunctionARN(functionID, lambda.FunctionName, lambda.FunctionRegion)
	lambda.InvocationType = req.Header.Get(lambdaInvocationType)
	lambda.ExecutedVersion = resp.Header.Get(lambdaExecutedVersion)

	// Service errors (throttling, missing function, ...): HTTP 4xx/5xx and
	// x-amzn-ErrorType: <ExceptionName>[:<namespace>].
	if resp.StatusCode >= http.StatusBadRequest {
		lambda.ErrorType, _, _ = strings.Cut(resp.Header.Get(amznErrorTypeHeader), ":")
	}

	// Function errors: HTTP 200 with x-amz-function-error and the runtime's
	// error payload as the response body.
	lambda.FunctionError = resp.Header.Get(lambdaFunctionErrorHeader)
	if lambda.FunctionError != "" && resp.Body != nil {
		respB, err := io.ReadAll(resp.Body)
		if err == nil {
			resp.Body = io.NopCloser(bytes.NewBuffer(respB))
			var p awsLambdaErrorPayload
			if json.Unmarshal(respB, &p) == nil {
				lambda.ErrorType = p.ErrorType
				lambda.ErrorMessage = p.ErrorMessage
			}
		}
		if lambda.ErrorType == "" {
			lambda.ErrorType = lambda.FunctionError
		}
	}

	return lambda, nil
}

// lambdaInvokeFunctionID returns the (unescaped) FunctionName path segment
// of an Invoke request: a name, a partial ARN or a full ARN, optionally
// followed by ":<qualifier>".
//
// The path may be cut short: the Go probes copy at most k_path_max_len
// (100) bytes of URL.Path, and Go requests rebuilt from HTTP/2 frames use
// that copy, so a full ARN with a long name loses the end of "/invocations".
// A path is still accepted when the FunctionName segment is complete and
// what follows is a prefix of "/invocations" long enough to tell it from the
// other /functions/<name>/... APIs.
func lambdaInvokeFunctionID(u *url.URL) (string, bool) {
	p, ok := strings.CutPrefix(u.EscapedPath(), lambdaInvokePathPrefix)
	if !ok {
		return "", false
	}
	seg, rest, ok := strings.Cut(p, "/")
	rest = "/" + rest
	if !ok || seg == "" || len(rest) < len(lambdaTruncatedSuffixMin) || !strings.HasPrefix(lambdaInvokePathSuffix, rest) {
		return "", false
	}
	id, err := url.PathUnescape(seg)
	if err != nil || id == "" {
		return "", false
	}
	return id, true
}

// parseLambdaFunctionID splits the FunctionName parameter of Invoke into the
// function name, the qualifier (version or alias) and, for a full ARN, the
// function's region. Accepted forms (https://docs.aws.amazon.com/lambda/latest/api/API_Invoke.html):
//
//	my-function                                                  => my-function, "", ""
//	my-function:prod                                             => my-function, prod, ""
//	123456789012:function:my-function[:prod]                     => my-function, prod, ""
//	arn:aws:lambda:us-west-2:123456789012:function:my-function   => my-function, "", us-west-2
//	arn:aws:lambda:us-west-2:123456789012:function:my-function:7 => my-function, 7, us-west-2
func parseLambdaFunctionID(id string) (name, qualifier, region string) {
	parts := strings.Split(id, ":")
	switch {
	case len(parts) >= 7 && parts[0] == "arn" && parts[2] == "lambda" && parts[5] == "function":
		// arn:<partition>:lambda:<region>:<account>:function:<name>[:<qualifier>]
		name, region = parts[6], parts[3]
		if len(parts) >= 8 {
			qualifier = parts[7]
		}
	case len(parts) >= 3 && parts[1] == "function":
		// <account>:function:<name>[:<qualifier>]
		name = parts[2]
		if len(parts) >= 4 {
			qualifier = parts[3]
		}
	case len(parts) <= 2:
		name = parts[0]
		if len(parts) == 2 {
			qualifier = parts[1]
		}
	}
	if region != "" && !isAWSRegion(region) {
		region = ""
	}
	return name, qualifier, region
}

var awsAccountIDRgx = regexp.MustCompile(`^\d{12}$`)

// lambdaFunctionARN returns the unqualified ARN of the invoked function when
// the FunctionName parameter names the account (full or partial ARN), else "".
// A call by bare name targets the caller's account, which OBI does not know;
// the collector can complete it from the cloud.account.id resource attribute.
//
//	arn:aws:lambda:us-west-2:123456789012:function:my-function:7 => arn:aws:lambda:us-west-2:123456789012:function:my-function
//	123456789012:function:my-function:prod (region us-west-2)    => arn:aws:lambda:us-west-2:123456789012:function:my-function
//	my-function                                                  => ""
func lambdaFunctionARN(id, name, region string) string {
	parts := strings.Split(id, ":")
	switch {
	case len(parts) >= 7 && parts[0] == "arn" && parts[2] == "lambda" && parts[5] == "function":
		return strings.Join(parts[:7], ":")
	case len(parts) >= 3 && parts[1] == "function" && awsAccountIDRgx.MatchString(parts[0]) && region != "":
		return "arn:" + awsPartition(region) + ":lambda:" + region + ":" + parts[0] + ":function:" + name
	}
	return ""
}

// awsPartition returns the ARN partition of a region (see isAWSRegion for the
// regions OBI recognizes).
func awsPartition(region string) string {
	switch {
	case strings.HasPrefix(region, "cn-"):
		return "aws-cn"
	case strings.HasPrefix(region, "us-gov-"):
		return "aws-us-gov"
	default:
		return "aws"
	}
}
