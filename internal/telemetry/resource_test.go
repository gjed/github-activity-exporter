package telemetry_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/gjed/github-activity-exporter/internal/telemetry"
)

func resourceValue(t *testing.T, r *resource.Resource, key string) string {
	t.Helper()
	v, ok := r.Set().Value(attribute.Key(key))
	if !ok {
		t.Fatalf("resource has no %s: %v", key, r.Attributes())
	}
	return v.AsString()
}

func TestNewResource_Defaults(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")

	r, err := telemetry.NewResource(context.Background(), "1.2.3")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}
	if got := resourceValue(t, r, "service.name"); got != "github-activity-exporter" {
		t.Errorf("service.name = %q", got)
	}
	if got := resourceValue(t, r, "service.version"); got != "1.2.3" {
		t.Errorf("service.version = %q", got)
	}
	if _, err := uuid.Parse(resourceValue(t, r, "service.instance.id")); err != nil {
		t.Errorf("service.instance.id is not a UUID: %v", err)
	}
	resourceValue(t, r, "process.pid")
}

func TestNewResource_EnvWins(t *testing.T) {
	t.Setenv("OTEL_SERVICE_NAME", "gh-exp")
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "service.name=ignored,service.instance.id=pod-1,deployment.environment.name=prod")

	r, err := telemetry.NewResource(context.Background(), "1.2.3")
	if err != nil {
		t.Fatalf("NewResource: %v", err)
	}
	for key, want := range map[string]string{
		"service.name":                "gh-exp",
		"service.instance.id":         "pod-1",
		"service.version":             "1.2.3",
		"deployment.environment.name": "prod",
	} {
		if got := resourceValue(t, r, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestNewResource_InstanceIDPerCall(t *testing.T) {
	t.Setenv("OTEL_RESOURCE_ATTRIBUTES", "")
	a, _ := telemetry.NewResource(context.Background(), "v")
	b, _ := telemetry.NewResource(context.Background(), "v")
	if resourceValue(t, a, "service.instance.id") == resourceValue(t, b, "service.instance.id") {
		t.Error("service.instance.id repeated across processes")
	}
}
