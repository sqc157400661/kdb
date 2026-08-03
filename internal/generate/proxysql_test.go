package generate

import (
	"strings"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestProxySQLManagerUsesPackagedBinaryEntrypoint(t *testing.T) {
	assertManagerEntrypoint := func(t *testing.T, containers []string) {
		t.Helper()
		if len(containers) != 1 || containers[0] != "/kdb/bin/manager" {
			t.Fatalf("ProxySQL manager command = %#v, want packaged sidecar binary", containers)
		}
	}

	proxy := DefaultedProxySpecForSpec(&v1.KDBProxySpec{Enabled: true})
	instanceContainers := proxySQLContainers(proxy, 6033, 6032)
	if len(instanceContainers) != 2 {
		t.Fatalf("instance ProxySQL containers = %d, want 2", len(instanceContainers))
	}
	assertManagerEntrypoint(t, instanceContainers[1].Command)
	if len(instanceContainers[0].Args) != 1 ||
		!strings.Contains(instanceContainers[0].Args[0], "admin-username") ||
		!strings.Contains(instanceContainers[0].Args[0], proxySQLAdminCredentialsPlaceholder) ||
		!strings.Contains(instanceContainers[0].Args[0], "exec proxysql -f -c") {
		t.Fatalf("ProxySQL startup command does not materialize Secret-backed runtime config: %#v", instanceContainers[0].Args)
	}
	mounts := map[string]bool{}
	for _, mount := range instanceContainers[0].VolumeMounts {
		mounts[mount.Name] = true
	}
	for _, name := range []string{"proxysql-config", "proxysql-secret", "proxysql-data", "proxysql-runtime"} {
		if !mounts[name] {
			t.Fatalf("ProxySQL container is missing volume mount %q", name)
		}
	}

	cluster := &v1.KDBCluster{
		ObjectMeta: metav1.ObjectMeta{Name: "mysql", Namespace: "kdb"},
		Spec:       v1.KDBClusterSpec{Proxy: &v1.KDBProxySpec{Enabled: true}},
	}
	deployment := &appsv1.Deployment{}
	ProxySQLDeploymentIntent(cluster, deployment, "test-version")
	if len(deployment.Spec.Template.Spec.Containers) != 2 {
		t.Fatalf("cluster ProxySQL containers = %d, want 2", len(deployment.Spec.Template.Spec.Containers))
	}
	assertManagerEntrypoint(t, deployment.Spec.Template.Spec.Containers[1].Command)
}

func TestProxySQLVolumesProjectUserPasswordSecret(t *testing.T) {
	volumes := proxySQLVolumes("proxy-config", "proxy-runtime-secret", []v1.KDBProxyUserSpec{{
		Username: "app_user",
		PasswordSecretRef: &corev1.SecretKeySelector{
			LocalObjectReference: corev1.LocalObjectReference{Name: "app-user-secret"},
			Key:                  "password",
		},
	}})
	var projected *corev1.ProjectedVolumeSource
	for _, volume := range volumes {
		if volume.Name == "proxysql-secret" {
			projected = volume.Projected
		}
	}
	if projected == nil || len(projected.Sources) != 2 {
		t.Fatalf("ProxySQL projected Secret sources = %#v, want runtime plus user Secret", projected)
	}
	userProjection := projected.Sources[1].Secret
	if userProjection == nil || userProjection.Name != "app-user-secret" || len(userProjection.Items) != 1 ||
		userProjection.Items[0].Key != "password" || userProjection.Items[0].Path != "users/app_user/password" {
		t.Fatalf("ProxySQL user Secret projection = %#v, want exact password file path", userProjection)
	}
}

func TestRenderProxySQLDesiredConfigRequiresUserSecretReference(t *testing.T) {
	instance := &v1.KDBInstance{
		Spec: v1.KDBInstanceSpec{Proxy: &v1.KDBProxySpec{
			Enabled: true,
			Config: &v1.KDBProxyConfigSpec{Inline: &v1.KDBProxyInlineConfigSpec{
				Users: []v1.KDBProxyUserSpec{{Username: "app_user"}},
			}},
		}},
	}
	if _, _, err := RenderProxySQLInstanceDesiredConfig(instance); err == nil || !strings.Contains(err.Error(), "requires passwordSecretRef") {
		t.Fatalf("render error = %v, want missing user Secret reference validation", err)
	}
}

func TestRenderProxySQLConfigUsesRuntimeCredentialPlaceholder(t *testing.T) {
	config := RenderProxySQLInstanceConfig(&v1.KDBInstance{
		Spec: v1.KDBInstanceSpec{Proxy: &v1.KDBProxySpec{Enabled: true}},
	})
	if !strings.Contains(config, `admin_credentials="`+proxySQLAdminCredentialsPlaceholder+`"`) {
		t.Fatalf("ProxySQL config does not contain runtime admin credential placeholder:\n%s", config)
	}
	if strings.Contains(config, "admin:admin") {
		t.Fatal("ProxySQL config contains a static default admin credential")
	}
}
