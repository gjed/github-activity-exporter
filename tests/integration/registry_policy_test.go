//go:build integration

package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestRegistryPolicy_PrometheusContractViolations runs `weaver registry check`
// with policies/registry/ on a fixture registry that breaks each rule of the
// annotations.prometheus contract, and asserts every rule reports its
// violation. Guards against the policy silently matching nothing, for example
// after a change of the resolved registry shape. test.imported is imported
// from a sibling dependency registry with no refinement declaring the annotation.
func TestRegistryPolicy_PrometheusContractViolations(t *testing.T) {
	runner := requireWeaverRunner(t)
	fixture, err := filepath.Abs(filepath.Join("testdata", "registry-bad-prometheus"))
	if err != nil {
		t.Fatalf("resolve fixture path: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	cmd := runner.registryCheckCmd(ctx, fixture)
	var out bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &out)
	cmd.Stderr = io.MultiWriter(os.Stderr, &out)
	err = cmd.Run()

	for _, msg := range []string{
		"Metric 'test.imported' does not declare annotations.prometheus.name.",
		"Metric 'test.imported' does not declare annotations.prometheus.type.",
		"Metric 'test.missing' does not declare annotations.prometheus.name.",
		"Metric 'test.missing' does not declare annotations.prometheus.type.",
		"Metric 'test.renamed' declares annotations.prometheus.name 'test.renamed_total'; it must equal the metric name.",
		"Metric 'test.retyped' (updowncounter) declares annotations.prometheus.type 'counter'; want 'gauge'.",
	} {
		requireViolation(t, out.String(), err, msg)
	}
}

// registryCheckCmd builds the exec.Cmd for `weaver registry check` on
// registryDir with the policies in policies/registry/. It runs in registryDir
// with the parent directory mounted alongside, so a fixture can depend on a
// sibling registry through a relative registry_path.
func (r weaverRunner) registryCheckCmd(ctx context.Context, registryDir string) *exec.Cmd {
	policies := filepath.Join(filepath.Dir(r.registryDir), "policies", "registry")
	args := []string{"registry", "check", "--v2"}
	if r.useDocker {
		fixture := "/workspace/testdata/" + filepath.Base(registryDir)
		docker := []string{
			"run", "--rm",
			"--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace/testdata,readonly", filepath.Dir(registryDir)),
			"--mount", fmt.Sprintf("type=bind,source=%s,target=/workspace/policies,readonly", policies),
			"--env", "HOME=/tmp/weaver",
			"--workdir", fixture,
			weaverImage,
		}
		args = append(append(docker, args...), "-r", fixture+"/", "-p", "/workspace/policies/")
		return exec.CommandContext(ctx, "docker", args...)
	}
	cmd := exec.CommandContext(ctx, "weaver", append(args, "-r", registryDir+"/", "-p", policies+"/")...)
	cmd.Dir = registryDir
	return cmd
}
