// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package ebpfcommon

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"go.opentelemetry.io/obi/pkg/appolly/app/request"
)

func lambdaReq(t *testing.T, method, rawURL string) *http.Request {
	t.Helper()
	r, err := http.NewRequest(method, rawURL, io.NopCloser(strings.NewReader(`{"k":"v"}`)))
	require.NoError(t, err)
	return r
}

func lambdaResp(status int, headers map[string]string, body string) *http.Response {
	r := &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestParseAWSLambda(t *testing.T) {
	okHeaders := map[string]string{"x-amzn-RequestId": "6b2f2c1a-0000-4000-8000-000000000001", "X-Amz-Executed-Version": "$LATEST"}

	tests := []struct {
		name     string
		req      *http.Request
		resp     *http.Response
		spanHost string
		want     request.AWSLambda
		wantErr  bool
	}{
		{
			name: "function name",
			req:  lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/my-function/invocations"),
			resp: lambdaResp(http.StatusOK, okHeaders, `{"ok":true}`),
			want: request.AWSLambda{
				Meta:            request.AWSMeta{RequestID: "6b2f2c1a-0000-4000-8000-000000000001", Region: "us-west-2"},
				Operation:       "Invoke",
				FunctionName:    "my-function",
				FunctionRegion:  "us-west-2",
				ExecutedVersion: "$LATEST",
			},
		},
		{
			name: "function name with qualifier query",
			req:  lambdaReq(t, http.MethodPost, "https://lambda.eu-central-1.amazonaws.com/2015-03-31/functions/my-function/invocations?Qualifier=prod"),
			resp: lambdaResp(http.StatusOK, okHeaders, ``),
			want: request.AWSLambda{
				Meta:            request.AWSMeta{RequestID: "6b2f2c1a-0000-4000-8000-000000000001", Region: "eu-central-1"},
				Operation:       "Invoke",
				FunctionName:    "my-function",
				Qualifier:       "prod",
				FunctionRegion:  "eu-central-1",
				ExecutedVersion: "$LATEST",
			},
		},
		{
			name: "function name with inline qualifier",
			req:  lambdaReq(t, http.MethodPost, "https://lambda.us-east-2.amazonaws.com/2015-03-31/functions/my-function%3A7/invocations"),
			resp: lambdaResp(http.StatusOK, okHeaders, ``),
			want: request.AWSLambda{
				Meta:            request.AWSMeta{RequestID: "6b2f2c1a-0000-4000-8000-000000000001", Region: "us-east-2"},
				Operation:       "Invoke",
				FunctionName:    "my-function",
				Qualifier:       "7",
				FunctionRegion:  "us-east-2",
				ExecutedVersion: "$LATEST",
			},
		},
		{
			name: "full ARN (percent-encoded) in another region",
			req:  lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/arn%3Aaws%3Alambda%3Aap-southeast-1%3A123456789012%3Afunction%3Amy-function/invocations"),
			resp: lambdaResp(http.StatusOK, okHeaders, ``),
			want: request.AWSLambda{
				Meta:            request.AWSMeta{RequestID: "6b2f2c1a-0000-4000-8000-000000000001", Region: "us-west-2"},
				Operation:       "Invoke",
				FunctionName:    "my-function",
				FunctionRegion:  "ap-southeast-1",
				FunctionARN:     "arn:aws:lambda:ap-southeast-1:123456789012:function:my-function",
				ExecutedVersion: "$LATEST",
			},
		},
		{
			name: "full ARN with alias, unescaped colons",
			req:  lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/arn:aws:lambda:us-west-2:123456789012:function:my-function:live/invocations"),
			resp: lambdaResp(http.StatusOK, okHeaders, ``),
			want: request.AWSLambda{
				Meta:            request.AWSMeta{RequestID: "6b2f2c1a-0000-4000-8000-000000000001", Region: "us-west-2"},
				Operation:       "Invoke",
				FunctionName:    "my-function",
				Qualifier:       "live",
				FunctionRegion:  "us-west-2",
				FunctionARN:     "arn:aws:lambda:us-west-2:123456789012:function:my-function",
				ExecutedVersion: "$LATEST",
			},
		},
		{
			name: "China partition ARN",
			req:  lambdaReq(t, http.MethodPost, "https://lambda.cn-north-1.amazonaws.com.cn/2015-03-31/functions/arn%3Aaws-cn%3Alambda%3Acn-north-1%3A123456789012%3Afunction%3Afn/invocations"),
			resp: lambdaResp(http.StatusOK, okHeaders, ``),
			want: request.AWSLambda{
				Meta:            request.AWSMeta{RequestID: "6b2f2c1a-0000-4000-8000-000000000001", Region: "cn-north-1"},
				Operation:       "Invoke",
				FunctionName:    "fn",
				FunctionRegion:  "cn-north-1",
				FunctionARN:     "arn:aws-cn:lambda:cn-north-1:123456789012:function:fn",
				ExecutedVersion: "$LATEST",
			},
		},
		{
			name: "partial ARN with version",
			req:  lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/123456789012%3Afunction%3Amy-function%3A3/invocations"),
			resp: lambdaResp(http.StatusOK, okHeaders, ``),
			want: request.AWSLambda{
				Meta:            request.AWSMeta{RequestID: "6b2f2c1a-0000-4000-8000-000000000001", Region: "us-west-2"},
				Operation:       "Invoke",
				FunctionName:    "my-function",
				Qualifier:       "3",
				FunctionRegion:  "us-west-2",
				FunctionARN:     "arn:aws:lambda:us-west-2:123456789012:function:my-function",
				ExecutedVersion: "$LATEST",
			},
		},
		{
			name: "asynchronous invocation",
			req: func() *http.Request {
				r := lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/my-function/invocations")
				r.Header.Set("X-Amz-Invocation-Type", "Event")
				return r
			}(),
			resp: lambdaResp(http.StatusAccepted, map[string]string{"X-Amzn-Requestid": "req-async"}, ``),
			want: request.AWSLambda{
				Meta:           request.AWSMeta{RequestID: "req-async", Region: "us-west-2"},
				Operation:      "Invoke",
				FunctionName:   "my-function",
				FunctionRegion: "us-west-2",
				InvocationType: "Event",
			},
		},
		{
			name: "unhandled function error",
			req:  lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/my-function/invocations"),
			resp: lambdaResp(http.StatusOK, map[string]string{"x-amzn-RequestId": "req-err", "X-Amz-Function-Error": "Unhandled"},
				`{"errorMessage": "boom", "errorType": "ValueError", "requestId": "req-err", "stackTrace": ["  File \"/var/task/app.py\""]}`),
			want: request.AWSLambda{
				Meta:           request.AWSMeta{RequestID: "req-err", Region: "us-west-2"},
				Operation:      "Invoke",
				FunctionName:   "my-function",
				FunctionRegion: "us-west-2",
				FunctionError:  "Unhandled",
				ErrorType:      "ValueError",
				ErrorMessage:   "boom",
			},
		},
		{
			name: "function error with truncated payload",
			req:  lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/my-function/invocations"),
			resp: lambdaResp(http.StatusOK, map[string]string{"x-amzn-RequestId": "req-err", "X-Amz-Function-Error": "Unhandled"}, `{"errorMessage": "bo`),
			want: request.AWSLambda{
				Meta:           request.AWSMeta{RequestID: "req-err", Region: "us-west-2"},
				Operation:      "Invoke",
				FunctionName:   "my-function",
				FunctionRegion: "us-west-2",
				FunctionError:  "Unhandled",
				ErrorType:      "Unhandled",
			},
		},
		{
			name: "service error: function not found",
			req:  lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/missing/invocations"),
			resp: lambdaResp(http.StatusNotFound, map[string]string{
				"x-amzn-RequestId": "req-404",
				"x-amzn-ErrorType": "ResourceNotFoundException:http://internal.amazon.com/coral/com.amazonaws.awslambda/",
			}, `{"Type":"User","message":"Function not found"}`),
			want: request.AWSLambda{
				Meta:           request.AWSMeta{RequestID: "req-404", Region: "us-west-2"},
				Operation:      "Invoke",
				FunctionName:   "missing",
				FunctionRegion: "us-west-2",
				ErrorType:      "ResourceNotFoundException",
			},
		},
		{
			// Go over HTTP/2: the request is rebuilt from frames with the peer IP as
			// Host and the probe's (truncated) URL.Path copy.
			name: "HTTP/2 request: span host and truncated path",
			req: func() *http.Request {
				r := lambdaReq(t, http.MethodPost, "https://18.246.199.104/2015-03-31/functions/arn:aws:lambda:us-west-2:123456789012:function:obi-lambda-test:live/i")
				r.Host = "18.246.199.104"
				return r
			}(),
			resp:     lambdaResp(http.StatusOK, okHeaders, ``),
			spanHost: "lambda.us-west-2.amazonaws.com",
			want: request.AWSLambda{
				Meta:            request.AWSMeta{RequestID: "6b2f2c1a-0000-4000-8000-000000000001", Region: "us-west-2"},
				Operation:       "Invoke",
				FunctionName:    "obi-lambda-test",
				Qualifier:       "live",
				FunctionRegion:  "us-west-2",
				FunctionARN:     "arn:aws:lambda:us-west-2:123456789012:function:obi-lambda-test",
				ExecutedVersion: "$LATEST",
			},
		},
		{
			name:     "span host does not override an AWS endpoint host",
			req:      lambdaReq(t, http.MethodPost, "https://lambda.eu-west-1.amazonaws.com/2015-03-31/functions/fn/invocation"),
			resp:     lambdaResp(http.StatusOK, okHeaders, ``),
			spanHost: "lambda.us-west-2.amazonaws.com",
			want: request.AWSLambda{
				Meta:            request.AWSMeta{RequestID: "6b2f2c1a-0000-4000-8000-000000000001", Region: "eu-west-1"},
				Operation:       "Invoke",
				FunctionName:    "fn",
				FunctionRegion:  "eu-west-1",
				ExecutedVersion: "$LATEST",
			},
		},
		{
			name:    "truncated path too short to be Invoke",
			req:     lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/my-function/"),
			resp:    lambdaResp(http.StatusOK, okHeaders, ``),
			wantErr: true,
		},
		{
			name:    "other Lambda API: PublishVersion",
			req:     lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/my-function/versions"),
			resp:    lambdaResp(http.StatusCreated, okHeaders, `{}`),
			wantErr: true,
		},
		{
			name:    "invocations followed by more path",
			req:     lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/my-function/invocations/x"),
			resp:    lambdaResp(http.StatusOK, okHeaders, ``),
			wantErr: true,
		},
		{
			name:    "missing request id",
			req:     lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/my-function/invocations"),
			resp:    lambdaResp(http.StatusOK, nil, ``),
			wantErr: true,
		},
		{
			name:    "other Lambda API: GetFunction",
			req:     lambdaReq(t, http.MethodGet, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/my-function"),
			resp:    lambdaResp(http.StatusOK, okHeaders, `{}`),
			wantErr: true,
		},
		{
			name:    "other Lambda API: InvokeAsync",
			req:     lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2014-11-13/functions/my-function/invoke-async/"),
			resp:    lambdaResp(http.StatusAccepted, okHeaders, ``),
			wantErr: true,
		},
		{
			name:    "other Lambda API: InvokeWithResponseStream",
			req:     lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2021-11-15/functions/my-function/response-streaming-invocations"),
			resp:    lambdaResp(http.StatusOK, okHeaders, ``),
			wantErr: true,
		},
		{
			name:    "GET on the invocations path",
			req:     lambdaReq(t, http.MethodGet, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/my-function/invocations"),
			resp:    lambdaResp(http.StatusOK, okHeaders, ``),
			wantErr: true,
		},
		{
			name:    "empty function name",
			req:     lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions//invocations"),
			resp:    lambdaResp(http.StatusOK, okHeaders, ``),
			wantErr: true,
		},
		{
			name:    "nested path",
			req:     lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/a/b/invocations"),
			resp:    lambdaResp(http.StatusOK, okHeaders, ``),
			wantErr: true,
		},
		{
			name:    "SQS request",
			req:     lambdaReq(t, http.MethodPost, "https://sqs.us-east-1.amazonaws.com/"),
			resp:    lambdaResp(http.StatusOK, map[string]string{"x-amzn-RequestId": "req-sqs"}, `{}`),
			wantErr: true,
		},
		{
			name:    "S3 request",
			req:     lambdaReq(t, http.MethodPut, "https://my-bucket.s3.us-west-2.amazonaws.com/key"),
			resp:    lambdaResp(http.StatusOK, map[string]string{"x-amz-request-id": "r", "x-amz-id-2": "e"}, ``),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseAWSLambda(tt.req, tt.resp, tt.spanHost)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseLambdaFunctionID(t *testing.T) {
	tests := []struct {
		id, name, qualifier, region string
	}{
		{"my-function", "my-function", "", ""},
		{"my-function:$LATEST", "my-function", "$LATEST", ""},
		{"123456789012:function:my-function", "my-function", "", ""},
		{"123456789012:function:my-function:prod", "my-function", "prod", ""},
		{"arn:aws:lambda:us-west-2:123456789012:function:my-function", "my-function", "", "us-west-2"},
		{"arn:aws:lambda:us-west-2:123456789012:function:my-function:1", "my-function", "1", "us-west-2"},
		{"arn:aws-us-gov:lambda:us-gov-west-1:123456789012:function:f", "f", "", "us-gov-west-1"},
		{"a:b:c", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			name, qualifier, region := parseLambdaFunctionID(tt.id)
			assert.Equal(t, tt.name, name)
			assert.Equal(t, tt.qualifier, qualifier)
			assert.Equal(t, tt.region, region)
		})
	}
}

func TestLambdaFunctionARN(t *testing.T) {
	tests := []struct {
		id, region, want string
	}{
		{"my-function", "us-west-2", ""},
		{"my-function:prod", "us-west-2", ""},
		{"123456789012:function:my-function", "us-west-2", "arn:aws:lambda:us-west-2:123456789012:function:my-function"},
		{"123456789012:function:my-function:prod", "eu-west-1", "arn:aws:lambda:eu-west-1:123456789012:function:my-function"},
		{"123456789012:function:my-function", "cn-northwest-1", "arn:aws-cn:lambda:cn-northwest-1:123456789012:function:my-function"},
		{"123456789012:function:my-function", "us-gov-west-1", "arn:aws-us-gov:lambda:us-gov-west-1:123456789012:function:my-function"},
		{"12345:function:my-function", "us-west-2", ""},
		{"123456789012:function:my-function", "", ""},
		{"arn:aws:lambda:us-west-2:123456789012:function:my-function", "eu-west-1", "arn:aws:lambda:us-west-2:123456789012:function:my-function"},
		{"arn:aws:lambda:us-west-2:123456789012:function:my-function:$LATEST", "us-west-2", "arn:aws:lambda:us-west-2:123456789012:function:my-function"},
		{"arn:aws-us-gov:lambda:us-gov-west-1:123456789012:function:f:live", "us-gov-west-1", "arn:aws-us-gov:lambda:us-gov-west-1:123456789012:function:f"},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			name, _, _ := parseLambdaFunctionID(tt.id)
			assert.Equal(t, tt.want, lambdaFunctionARN(tt.id, name, tt.region))
		})
	}
}

func TestAWSLambdaSpan(t *testing.T) {
	base := request.Span{Type: request.EventTypeHTTPClient, Method: http.MethodPost, Status: http.StatusOK}

	req := lambdaReq(t, http.MethodPost, "https://lambda.us-west-2.amazonaws.com/2015-03-31/functions/my-function/invocations")
	resp := lambdaResp(http.StatusOK, map[string]string{"x-amzn-RequestId": "req-1"}, `{"ok":true}`)
	span, ok := AWSLambdaSpan(&base, req, resp)
	require.True(t, ok)
	assert.Equal(t, request.HTTPSubtypeAWSLambda, span.SubType)
	require.NotNil(t, span.AWS)
	assert.Equal(t, "my-function", span.AWS.Lambda.FunctionName)
	assert.Equal(t, "lambda.Invoke", span.TraceName())

	// the response body stays readable for later parsers
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.JSONEq(t, `{"ok":true}`, string(b))

	other := request.Span{Type: request.EventTypeHTTPClient}
	span, ok = AWSLambdaSpan(&other, lambdaReq(t, http.MethodGet, "https://example.com/"), lambdaResp(http.StatusOK, nil, ``))
	assert.False(t, ok)
	assert.Equal(t, request.HTTPSubtypeNone, span.SubType)
	assert.Nil(t, span.AWS)
}
