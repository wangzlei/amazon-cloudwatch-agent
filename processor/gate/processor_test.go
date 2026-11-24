// Copyright Amazon.com, Inc. or its affiliates. All Rights Reserved.
// SPDX-License-Identifier: MIT

package gate

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/collector/component"
	"go.opentelemetry.io/collector/component/componenttest"
	"go.opentelemetry.io/collector/confmap"
	"go.opentelemetry.io/collector/consumer/consumertest"
	"go.opentelemetry.io/collector/extension/extensiontest"
	"go.opentelemetry.io/collector/pdata/plog"
	"go.opentelemetry.io/collector/pdata/pmetric"
	"go.opentelemetry.io/collector/pdata/ptrace"
	"go.opentelemetry.io/collector/processor/processortest"

	"github.com/aws/amazon-cloudwatch-agent/extension/runtimecontrol"
)

// fakeFlags implements runtimecontrol.Flags.
type fakeFlags struct {
	mu         sync.Mutex
	values     map[string]bool
	registered []string
}

func (f *fakeFlags) Flag(name string) (bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.values[name]
	return v, ok
}

func (f *fakeFlags) Register(name string) {
	f.mu.Lock()
	f.registered = append(f.registered, name)
	f.mu.Unlock()
}

func (f *fakeFlags) set(name string, v bool) { f.mu.Lock(); f.values[name] = v; f.mu.Unlock() }
func (f *fakeFlags) unset(name string)       { f.mu.Lock(); delete(f.values, name); f.mu.Unlock() }

type fakeExt struct {
	component.StartFunc
	component.ShutdownFunc
	*fakeFlags
}

type other struct {
	component.StartFunc
	component.ShutdownFunc
}

type host struct {
	component.Host
	exts map[component.ID]component.Component
}

func (h host) GetExtensions() map[component.ID]component.Component { return h.exts }

var rcID = component.MustNewIDWithName("runtimecontrol", "cwagent_ebpf")

func traces() ptrace.Traces {
	td := ptrace.NewTraces()
	td.ResourceSpans().AppendEmpty().ScopeSpans().AppendEmpty().Spans().AppendEmpty().SetName("GET /")
	return td
}

func metrics() pmetric.Metrics {
	md := pmetric.NewMetrics()
	md.ResourceMetrics().AppendEmpty().ScopeMetrics().AppendEmpty().Metrics().AppendEmpty().SetName("m")
	return md
}

func logs() plog.Logs {
	ld := plog.NewLogs()
	ld.ResourceLogs().AppendEmpty().ScopeLogs().AppendEmpty().LogRecords().AppendEmpty()
	return ld
}

func TestGateOnOff(t *testing.T) {
	ff := &fakeFlags{values: map[string]bool{}}
	h := host{Host: componenttest.NewNopHost(), exts: map[component.ID]component.Component{
		rcID:                         fakeExt{fakeFlags: ff},
		component.MustNewID("other"): other{},
	}}
	f := NewFactory()
	cfg := &Config{Flag: "traces_export", Default: false}
	ctx := context.Background()

	tsink, msink, lsink := new(consumertest.TracesSink), new(consumertest.MetricsSink), new(consumertest.LogsSink)
	tp, err := f.CreateTraces(ctx, processortest.NewNopSettings(TypeStr), cfg, tsink)
	require.NoError(t, err)
	mp, err := f.CreateMetrics(ctx, processortest.NewNopSettings(TypeStr), cfg, msink)
	require.NoError(t, err)
	lp, err := f.CreateLogs(ctx, processortest.NewNopSettings(TypeStr), cfg, lsink)
	require.NoError(t, err)
	assert.False(t, tp.Capabilities().MutatesData)
	for _, p := range []component.Component{tp, mp, lp} {
		require.NoError(t, p.Start(ctx, h))
	}
	assert.Equal(t, []string{"traces_export", "traces_export", "traces_export"}, ff.registered)

	send := func() {
		require.NoError(t, tp.ConsumeTraces(ctx, traces()))
		require.NoError(t, mp.ConsumeMetrics(ctx, metrics()))
		require.NoError(t, lp.ConsumeLogs(ctx, logs()))
	}
	counts := func() []int {
		return []int{len(tsink.AllTraces()), len(msink.AllMetrics()), len(lsink.AllLogs())}
	}

	send() // unknown -> default false: dropped, no error to the caller
	assert.Equal(t, []int{0, 0, 0}, counts())
	ff.set("traces_export", true)
	send()
	assert.Equal(t, []int{1, 1, 1}, counts())
	ff.set("traces_export", false)
	send()
	assert.Equal(t, []int{1, 1, 1}, counts())
	ff.set("traces_export", true)
	send()
	send()
	assert.Equal(t, []int{3, 3, 3}, counts())
	ff.unset("traces_export") // back to the default
	send()
	assert.Equal(t, []int{3, 3, 3}, counts())
	ff.set("other_flag", true)
	send()
	assert.Equal(t, []int{3, 3, 3}, counts())
}

func TestGateDefault(t *testing.T) {
	ctx := context.Background()
	f := NewFactory()
	for _, tc := range []struct {
		name string
		h    component.Host
		def  bool
		want int
	}{
		{"no extension, default false", componenttest.NewNopHost(), false, 0},
		{"no extension, default true", componenttest.NewNopHost(), true, 1},
		{"switch unknown, default true", host{exts: map[component.ID]component.Component{rcID: fakeExt{fakeFlags: &fakeFlags{values: map[string]bool{}}}}}, true, 1},
		{"switch false overrides default true", host{exts: map[component.ID]component.Component{rcID: fakeExt{fakeFlags: &fakeFlags{values: map[string]bool{"x": false}}}}}, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sink := new(consumertest.TracesSink)
			p, err := f.CreateTraces(ctx, processortest.NewNopSettings(TypeStr), &Config{Flag: "x", Default: tc.def}, sink)
			require.NoError(t, err)
			require.NoError(t, p.Start(ctx, tc.h))
			require.NoError(t, p.ConsumeTraces(ctx, traces()))
			assert.Len(t, sink.AllTraces(), tc.want)
		})
	}
}

func TestGateExtensionSelection(t *testing.T) {
	ctx := context.Background()
	f := NewFactory()
	a := fakeExt{fakeFlags: &fakeFlags{values: map[string]bool{"x": true}}}
	b := fakeExt{fakeFlags: &fakeFlags{values: map[string]bool{"x": false}}}
	bID := component.MustNewIDWithName("runtimecontrol", "b")
	two := host{exts: map[component.ID]component.Component{rcID: a, bID: b, component.MustNewID("other"): other{}}}

	p, err := f.CreateTraces(ctx, processortest.NewNopSettings(TypeStr), &Config{Flag: "x"}, consumertest.NewNop())
	require.NoError(t, err)
	assert.ErrorContains(t, p.Start(ctx, two), "several runtimecontrol extensions")

	sink := new(consumertest.TracesSink)
	p, err = f.CreateTraces(ctx, processortest.NewNopSettings(TypeStr), &Config{Flag: "x", Extension: &bID}, sink)
	require.NoError(t, err)
	require.NoError(t, p.Start(ctx, two))
	require.NoError(t, p.ConsumeTraces(ctx, traces()))
	assert.Empty(t, sink.AllTraces(), "uses runtimecontrol/b (x=false)")

	otherID := component.MustNewID("other")
	missing := component.MustNewID("runtimecontrol")
	for _, id := range []*component.ID{&otherID, &missing} {
		p, err = f.CreateTraces(ctx, processortest.NewNopSettings(TypeStr), &Config{Flag: "x", Extension: id}, consumertest.NewNop())
		require.NoError(t, err)
		assert.Error(t, p.Start(ctx, two), id.String())
	}
}

func TestConfig(t *testing.T) {
	cfg := NewFactory().CreateDefaultConfig().(*Config)
	assert.Error(t, cfg.Validate(), "flag is required")
	require.NoError(t, confmap.NewFromStringMap(map[string]any{"flag": "emf_export", "default": true, "extension": "runtimecontrol/cwagent_ebpf"}).Unmarshal(cfg))
	assert.NoError(t, cfg.Validate())
	assert.Equal(t, &Config{Flag: "emf_export", Default: true, Extension: &rcID}, cfg)
}

// With the real extension and no parameter (disabled): the default applies.
func TestGateWithDisabledRuntimeControl(t *testing.T) {
	ctx := context.Background()
	rf := runtimecontrol.NewFactory()
	ext, err := rf.Create(ctx, extensiontest.NewNopSettings(runtimecontrol.TypeStr), rf.CreateDefaultConfig())
	require.NoError(t, err)
	require.NoError(t, ext.Start(ctx, componenttest.NewNopHost()))
	defer func() { require.NoError(t, ext.Shutdown(ctx)) }()
	h := host{exts: map[component.ID]component.Component{rcID: ext}}
	for _, def := range []bool{false, true} {
		sink := new(consumertest.MetricsSink)
		p, err := NewFactory().CreateMetrics(ctx, processortest.NewNopSettings(TypeStr), &Config{Flag: "emf_export", Default: def}, sink)
		require.NoError(t, err)
		require.NoError(t, p.Start(ctx, h))
		require.NoError(t, p.ConsumeMetrics(ctx, metrics()))
		assert.Equal(t, map[bool]int{false: 0, true: 1}[def], len(sink.AllMetrics()))
	}
}
