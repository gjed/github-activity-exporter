package collector

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	tracenoop "go.opentelemetry.io/otel/trace/noop"

	"github.com/gjed/github-activity-exporter/internal/discovery"
	ghclient "github.com/gjed/github-activity-exporter/internal/github"
	sc "github.com/gjed/github-activity-exporter/internal/semconv"
)

// attrErrorType is the OTel error.type attribute, which has no constant in
// internal/semconv.
const attrErrorType = "error.type"

// noopTracer records nothing; it stands in for a nil tracer.
var noopTracer trace.Tracer = tracenoop.NewTracerProvider().Tracer("")

// noopSpan is the span handed out when tracing is off.
var noopSpan = trace.SpanFromContext(context.Background())

// startSpan starts a span of t named name under ctx, with the options
// opts builds. With the no-op tracer it returns ctx and a non-recording span
// without building the options, so traces off cost no allocation.
func startSpan(ctx context.Context, t trace.Tracer, name string, opts func() []trace.SpanStartOption) (context.Context, trace.Span) {
	if t == noopTracer {
		return ctx, noopSpan
	}
	return t.Start(ctx, name, opts()...)
}

// tracerOrNoop returns t, or the no-op tracer when t is nil.
func tracerOrNoop(t trace.Tracer) trace.Tracer {
	if t == nil {
		return noopTracer
	}
	return t
}

// startRepo starts the github_exporter.repo span of one repo unit. ctx must
// be the context the unit's closure captured, so the span stays in the cycle
// trace whatever goroutine runs the unit.
func startRepo(ctx context.Context, t trace.Tracer, repo discovery.Repo) (context.Context, trace.Span) {
	return startSpan(ctx, t, sc.SpanGitHubExporterRepo, func() []trace.SpanStartOption {
		return []trace.SpanStartOption{trace.WithAttributes(
			attribute.String(sc.AttrGitHubOrg, repo.Owner),
			attribute.String(sc.AttrGitHubRepo, repo.Name),
		)}
	})
}

// endRepo ends a github_exporter.repo span, failed when err is set.
func endRepo(span trace.Span, err error) {
	if err != nil && span.IsRecording() {
		setFailed(span, ghclient.ErrorType(err), err.Error())
	}
	span.End()
}

// setFailed marks span as failed with error.type errType.
func setFailed(span trace.Span, errType, description string) {
	span.SetStatus(codes.Error, description)
	span.SetAttributes(attribute.String(attrErrorType, errType))
}

// endBatch ends a github_exporter.batch span with the number of failed repos
// of the batch, failed with the type of the first failure when there is one.
func endBatch(span trace.Span, errs []error) {
	if !span.IsRecording() {
		return
	}
	defer span.End()
	failed := 0
	var first error
	for _, err := range errs {
		if err != nil {
			failed++
			if first == nil {
				first = err
			}
		}
	}
	span.SetAttributes(attribute.Int(sc.AttrGitHubExporterFailedRepos, failed))
	if first != nil {
		setFailed(span, ghclient.ErrorType(first), first.Error())
	}
}
