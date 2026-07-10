package k8s

import (
	"encoding/base64"
	"regexp"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"

	"github.com/gastownhall/gascity/internal/runtime"
)

func TestBuildPod_NodeSelector(t *testing.T) {
	p := newProviderWithOps(newFakeK8sOps())
	p.nodeSelector = map[string]string{"workload": "gc-agents"}
	pod, err := buildPod("test-session", runtime.Config{Command: "/bin/bash"}, p)
	if err != nil {
		t.Fatalf("buildPod: %v", err)
	}
	if pod.Spec.NodeSelector["workload"] != "gc-agents" {
		t.Errorf("NodeSelector[workload] = %q, want \"gc-agents\"", pod.Spec.NodeSelector["workload"])
	}
}

func TestBuildPod_Tolerations(t *testing.T) {
	p := newProviderWithOps(newFakeK8sOps())
	p.tolerations = []corev1.Toleration{{
		Key: "gc-agents", Operator: corev1.TolerationOpExists, Effect: corev1.TaintEffectNoSchedule,
	}}
	pod, err := buildPod("test-session", runtime.Config{Command: "/bin/bash"}, p)
	if err != nil {
		t.Fatalf("buildPod: %v", err)
	}
	if len(pod.Spec.Tolerations) != 1 {
		t.Fatalf("len(Tolerations) = %d, want 1", len(pod.Spec.Tolerations))
	}
	if pod.Spec.Tolerations[0].Key != "gc-agents" {
		t.Errorf("Toleration.Key = %q, want \"gc-agents\"", pod.Spec.Tolerations[0].Key)
	}
}

func TestBuildPod_Affinity(t *testing.T) {
	p := newProviderWithOps(newFakeK8sOps())
	p.affinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key: "node-type", Operator: corev1.NodeSelectorOpIn, Values: []string{"gpu"},
					}},
				}},
			},
		},
	}
	pod, err := buildPod("test-session", runtime.Config{Command: "/bin/bash"}, p)
	if err != nil {
		t.Fatalf("buildPod: %v", err)
	}
	if pod.Spec.Affinity == nil {
		t.Fatal("Affinity is nil")
	}
	if pod.Spec.Affinity.NodeAffinity == nil {
		t.Fatal("NodeAffinity is nil")
	}
	expressions := pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions
	if expressions[0].Values[0] != "gpu" {
		t.Fatalf("affinity value = %q, want gpu", expressions[0].Values[0])
	}
}

func TestBuildPod_PriorityClassName(t *testing.T) {
	p := newProviderWithOps(newFakeK8sOps())
	p.priorityClassName = "gc-agent-high"
	pod, err := buildPod("test-session", runtime.Config{Command: "/bin/bash"}, p)
	if err != nil {
		t.Fatalf("buildPod: %v", err)
	}
	if pod.Spec.PriorityClassName != "gc-agent-high" {
		t.Errorf("PriorityClassName = %q, want \"gc-agent-high\"", pod.Spec.PriorityClassName)
	}
}

func TestBuildPod_ExtraProjectionFields(t *testing.T) {
	mode := int32(365)
	p := newProviderWithOps(newFakeK8sOps())
	p.extraVolumes = []corev1.Volume{{
		Name: "agent-tools",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: "agent-tools"},
				DefaultMode:          &mode,
			},
		},
	}}
	p.extraVolumeMounts = []corev1.VolumeMount{{
		Name:      "agent-tools",
		MountPath: "/opt/agent-tools",
		ReadOnly:  true,
	}}
	p.extraEnv = []corev1.EnvVar{{
		Name: "PROVIDER_API_KEY",
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: "provider-api-key"},
				Key:                  "PROVIDER_API_KEY",
			},
		},
	}}

	pod, err := buildPod("test-session", runtime.Config{
		Command: "/bin/bash",
		Env: map[string]string{
			"PROVIDER_API_KEY": "literal-controller-copy",
		},
	}, p)
	if err != nil {
		t.Fatalf("buildPod: %v", err)
	}

	if pod.Spec.Volumes[len(pod.Spec.Volumes)-1].Name != "agent-tools" {
		t.Fatalf("last volume = %q, want agent-tools", pod.Spec.Volumes[len(pod.Spec.Volumes)-1].Name)
	}
	container := pod.Spec.Containers[0]
	foundMount := false
	for _, mount := range container.VolumeMounts {
		if mount.Name == "agent-tools" {
			foundMount = true
			if mount.MountPath != "/opt/agent-tools" || !mount.ReadOnly {
				t.Fatalf("tool mount = %#v, want read-only /opt/agent-tools", mount)
			}
		}
	}
	if !foundMount {
		t.Fatal("missing agent-tools volume mount")
	}

	var providerEnv []corev1.EnvVar
	for _, entry := range container.Env {
		if entry.Name == "PROVIDER_API_KEY" {
			providerEnv = append(providerEnv, entry)
		}
	}
	if len(providerEnv) != 1 {
		t.Fatalf("PROVIDER_API_KEY env count = %d, want 1", len(providerEnv))
	}
	if providerEnv[0].Value != "" {
		t.Fatalf("PROVIDER_API_KEY literal value = %q, want empty", providerEnv[0].Value)
	}
	if providerEnv[0].ValueFrom == nil || providerEnv[0].ValueFrom.SecretKeyRef == nil {
		t.Fatalf("PROVIDER_API_KEY = %#v, want SecretKeyRef", providerEnv[0])
	}
	if providerEnv[0].ValueFrom.SecretKeyRef.Name != "provider-api-key" {
		t.Fatalf("SecretKeyRef name = %q, want provider-api-key", providerEnv[0].ValueFrom.SecretKeyRef.Name)
	}
}

func TestBuildPod_DynamicUserPreservesEnvForStartupScript(t *testing.T) {
	p := newProviderWithOps(newFakeK8sOps())
	p.prebaked = true
	p.extraEnv = []corev1.EnvVar{
		{Name: "PATH", Value: "/opt/gr7n-agent-tools:/home/ubuntu/bin:/usr/local/bin:/usr/bin:/bin"},
		{Name: "CLAUDE_CONFIG_DIR", Value: "/home/ubuntu/.claude"},
	}

	pod, err := buildPod("test-session", runtime.Config{
		Command:  "gc agent-script --script /workspace/rig/worker.yaml",
		PreStart: []string{"echo pre-start"},
		WorkDir:  "/host/city/.gc/agents/k8s-canary",
		Env: map[string]string{
			"GC_CITY":        "/host/city",
			"LINUX_USERNAME": "ubuntu",
		},
	}, p)
	if err != nil {
		t.Fatalf("buildPod: %v", err)
	}

	container := pod.Spec.Containers[0]
	if container.SecurityContext == nil || container.SecurityContext.RunAsUser == nil || *container.SecurityContext.RunAsUser != 0 {
		t.Fatalf("RunAsUser = %#v, want root for dynamic user setup", container.SecurityContext)
	}
	if got := pod.Annotations[podLinuxUsernameAnnotation]; got != "ubuntu" {
		t.Fatalf("%s annotation = %q, want ubuntu", podLinuxUsernameAnnotation, got)
	}
	args := container.Args[0]
	if !strings.Contains(args, "su -m ubuntu -c") {
		t.Fatalf("entrypoint does not preserve env through su -m:\n%s", args)
	}
	if strings.Contains(args, "su - ubuntu -c") {
		t.Fatalf("entrypoint uses login su that drops env:\n%s", args)
	}

	match := regexp.MustCompile(`echo '([^']+)' \| base64 -d > "\$START_SCRIPT"`).FindStringSubmatch(args)
	if len(match) != 2 {
		t.Fatalf("entrypoint missing encoded startup script:\n%s", args)
	}
	decoded, err := base64.StdEncoding.DecodeString(match[1])
	if err != nil {
		t.Fatalf("decoding startup script: %v", err)
	}
	inner := string(decoded)
	for _, want := range []string{
		"export HOME='/home/ubuntu'",
		"export CLAUDE_CONFIG_DIR='/home/ubuntu/.claude'",
		"export PATH='/opt/gr7n-agent-tools:/home/ubuntu/bin:/usr/local/bin:/usr/bin:/bin'",
		"cd /workspace/.gc/agents/k8s-canary",
		"mkdir -p $HOME/.claude",
		"git config --global --add safe.directory '*'",
		"echo 'ZWNobyBwcmUtc3RhcnQ=' | base64 -d | sh",
		"tmux new-session -d -s main",
	} {
		if !strings.Contains(inner, want) {
			t.Fatalf("startup script missing %q:\n%s", want, inner)
		}
	}

	foundClaudeDir := false
	for _, env := range container.Env {
		if env.Name == "CLAUDE_CONFIG_DIR" {
			foundClaudeDir = true
			if env.Value != "/home/ubuntu/.claude" {
				t.Fatalf("CLAUDE_CONFIG_DIR = %q, want /home/ubuntu/.claude", env.Value)
			}
		}
	}
	if !foundClaudeDir {
		t.Fatal("missing CLAUDE_CONFIG_DIR env")
	}
}

func TestBuildPod_NoSchedulingFields_NoBehaviorChange(t *testing.T) {
	// Zero-value scheduling fields must not alter default pod behavior.
	p := newProviderWithOps(newFakeK8sOps())
	pod, err := buildPod("test-session", runtime.Config{Command: "/bin/bash"}, p)
	if err != nil {
		t.Fatalf("buildPod: %v", err)
	}
	if pod.Spec.NodeSelector != nil {
		t.Errorf("NodeSelector should be nil when not set")
	}
	if len(pod.Spec.Tolerations) != 0 {
		t.Errorf("Tolerations should be empty when not set")
	}
	if pod.Spec.Affinity != nil {
		t.Errorf("Affinity should be nil when not set")
	}
	if pod.Spec.PriorityClassName != "" {
		t.Errorf("PriorityClassName should be empty when not set")
	}
}

func TestBuildPod_ClonesSchedulingFields(t *testing.T) {
	seconds := int64(30)
	p := newProviderWithOps(newFakeK8sOps())
	p.nodeSelector = map[string]string{"workload": "gc-agents"}
	p.tolerations = []corev1.Toleration{{
		Key:               "gc-agents",
		Operator:          corev1.TolerationOpExists,
		Effect:            corev1.TaintEffectNoSchedule,
		TolerationSeconds: &seconds,
	}}
	p.affinity = &corev1.Affinity{
		NodeAffinity: &corev1.NodeAffinity{
			RequiredDuringSchedulingIgnoredDuringExecution: &corev1.NodeSelector{
				NodeSelectorTerms: []corev1.NodeSelectorTerm{{
					MatchExpressions: []corev1.NodeSelectorRequirement{{
						Key: "node-type", Operator: corev1.NodeSelectorOpIn, Values: []string{"gpu"},
					}},
				}},
			},
		},
	}

	pod, err := buildPod("test-session", runtime.Config{Command: "/bin/bash"}, p)
	if err != nil {
		t.Fatalf("buildPod: %v", err)
	}

	pod.Spec.NodeSelector["workload"] = "changed"
	pod.Spec.Tolerations[0].Key = "changed"
	pod.Spec.Affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Values[0] = "changed"

	if p.nodeSelector["workload"] != "gc-agents" {
		t.Fatalf("provider nodeSelector mutated to %q", p.nodeSelector["workload"])
	}
	if p.tolerations[0].Key != "gc-agents" {
		t.Fatalf("provider toleration key mutated to %q", p.tolerations[0].Key)
	}
	values := p.affinity.NodeAffinity.RequiredDuringSchedulingIgnoredDuringExecution.NodeSelectorTerms[0].MatchExpressions[0].Values
	if values[0] != "gpu" {
		t.Fatalf("provider affinity value mutated to %q", values[0])
	}
}
