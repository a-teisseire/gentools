// Code generated by mongen. DO NOT EDIT.
package examplesmws

import (
	alias4 "context"
	alias1 "github.com/Bo0mer/gentools/cmd/mongen/examples"
	alias2 "github.com/go-kit/kit/metrics"
	alias3 "time"
)

type monitoringGoKitService struct {
	next        alias1.GoKitService
	totalOps    alias2.Counter
	failedOps   alias2.Counter
	opsDuration alias2.Histogram
}

// NewMonitoringGoKitService creates new monitoring middleware.
func NewMonitoringGoKitService(next alias1.GoKitService, totalOps alias2.Counter, failedOps alias2.Counter, opsDuration alias2.Histogram) alias1.GoKitService {
	return &monitoringGoKitService{next, totalOps, failedOps, opsDuration}
}
func (m *monitoringGoKitService) DoWork(arg1 int, arg2 string) (string, error) {
	m.totalOps.With("operation", "do_work").Add(1)
	_start := alias3.Now()
	result1, result2 := m.next.DoWork(arg1, arg2)
	m.opsDuration.With("operation", "do_work").Observe(alias3.Since(_start).Seconds())
	if result2 != nil {
		m.failedOps.With("operation", "do_work").Add(1)
	}
	return result1, result2
}
func (m *monitoringGoKitService) DoWorkCtx(arg1 alias4.Context, arg2 int, arg3 string) (string, error) {
	m.totalOps.With("operation", "do_work_ctx").Add(1)
	_start := alias3.Now()
	result1, result2 := m.next.DoWorkCtx(arg1, arg2, arg3)
	m.opsDuration.With("operation", "do_work_ctx").Observe(alias3.Since(_start).Seconds())
	if result2 != nil {
		m.failedOps.With("operation", "do_work_ctx").Add(1)
	}
	return result1, result2
}
