// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package configtest

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/consumer"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/aws/amazon-cloudwatch-agent/service/defaultcomponents"
)

// The EMF pipeline's processors (after the gate) turn OBI's cumulative HTTP duration
// histograms into <name>_count / <name>_sum sums and drop everything else.
func TestRPMDefaultEBPFConfigEMFProcessors(t *testing.T) {
	setCreds(t)
	conf := resolve(t)
	factories, err := defaultcomponents.Factories()
	require.NoError(t, err)
	ctx := context.Background()

	sink := new(consumertest.MetricsSink)
	var next consumer.Metrics = sink
	procs := conf.Get("service::pipelines::metrics/cwagent_ebpf_emf::processors").([]any)
	for i := len(procs) - 1; i >= 1; i-- { // [0] is the gate
		var id component.ID
		require.NoError(t, id.UnmarshalText([]byte(procs[i].(string))))
		f := factories.Processors[id.Type()]
		cfg := f.CreateDefaultConfig()
		sub, err := conf.Sub("processors::" + id.String())
		require.NoError(t, err)
		require.NoError(t, sub.Unmarshal(cfg))
		set := processortest.NewNopSettings(id.Type())
		set.ID = id
		p, err := f.CreateMetrics(ctx, set, cfg, next)
		require.NoError(t, err, id.String())
		require.NoError(t, p.Start(ctx, nil))
		next = p
	}

	md := pmetric.NewMetrics()
	rm := md.ResourceMetrics().AppendEmpty()
	rm.Resource().Attributes().PutStr("service.name", "svc")
	ms := rm.ScopeMetrics().AppendEmpty().Metrics()
	for _, n := range []string{"http.server.request.duration", "http.client.request.duration", "http.server.request.body.size", "rpc.server.duration"} {
		m := ms.AppendEmpty()
		m.SetName(n)
		h := m.SetEmptyHistogram()
		h.SetAggregationTemporality(pmetric.AggregationTemporalityCumulative)
		dp := h.DataPoints().AppendEmpty()
		dp.SetCount(10)
		dp.SetSum(2.5)
		dp.Attributes().PutInt("http.response.status_code", 200)
	}
	g := ms.AppendEmpty()
	g.SetName("obi.internal")
	g.SetEmptyGauge().DataPoints().AppendEmpty().SetIntValue(1)

	require.NoError(t, next.ConsumeMetrics(ctx, md))
	require.Len(t, sink.AllMetrics(), 1)
	out := sink.AllMetrics()[0].ResourceMetrics().At(0).ScopeMetrics().At(0).Metrics()
	var names []string
	for i := 0; i < out.Len(); i++ {
		m := out.At(i)
		names = append(names, m.Name())
		require.Equal(t, pmetric.MetricTypeSum, m.Type(), m.Name())
		assert.True(t, m.Sum().IsMonotonic())
		assert.Equal(t, pmetric.AggregationTemporalityCumulative, m.Sum().AggregationTemporality())
		assert.EqualValues(t, 200, m.Sum().DataPoints().At(0).Attributes().AsRaw()["http.response.status_code"])
	}
	sort.Strings(names)
	assert.Equal(t, []string{"http.client.request.duration_count", "http.client.request.duration_sum", "http.server.request.duration_count", "http.server.request.duration_sum"}, names)
}
