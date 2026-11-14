package telemetry

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/contrib/bridges/otelslog"
	"go.opentelemetry.io/otel/trace"
)

// Logger returns the logger for everything built after New. With logs
// inactive it logs to stdout through stdout alone, so the output is exactly
// what stdout writes. With logs active, records also go to the
// LoggerProvider at level and above, and stdout lines logged inside a span
// get trace_id and span_id. Errors of the OTLP exporters must not be logged
// with it: they would be exported through the exporter that failed.
func (t *Telemetry) Logger(stdout slog.Handler, level slog.Leveler) *slog.Logger {
	if t.lp == nil {
		return slog.New(stdout)
	}
	otlp := otelslog.NewHandler(ScopeName, otelslog.WithLoggerProvider(t.lp), otelslog.WithVersion(t.version))
	return slog.New(slog.NewMultiHandler(traceContextHandler{stdout}, levelHandler{otlp, level}))
}

// traceContextHandler adds the trace_id and span_id of the record's span
// context, when it has a valid one.
type traceContextHandler struct{ slog.Handler }

func (h traceContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(slog.String("trace_id", sc.TraceID().String()), slog.String("span_id", sc.SpanID().String()))
	}
	return h.Handler.Handle(ctx, r)
}

func (h traceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return traceContextHandler{h.Handler.WithAttrs(attrs)}
}

func (h traceContextHandler) WithGroup(name string) slog.Handler {
	return traceContextHandler{h.Handler.WithGroup(name)}
}

// levelHandler drops records below level: the SDK logger reports every
// level enabled.
type levelHandler struct {
	slog.Handler
	level slog.Leveler
}

func (h levelHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.level.Level() && h.Handler.Enabled(ctx, l)
}

func (h levelHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return levelHandler{h.Handler.WithAttrs(attrs), h.level}
}

func (h levelHandler) WithGroup(name string) slog.Handler {
	return levelHandler{h.Handler.WithGroup(name), h.level}
}
