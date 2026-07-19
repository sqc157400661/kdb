package security

import (
	"crypto/x509"
	"encoding/pem"
	"strings"
	"testing"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGenerateRuntimeMTLSBundleSeparatesServerAndGatewayIdentities(t *testing.T) {
	instance := &v1.KDBInstance{ObjectMeta: metav1.ObjectMeta{Name: "orders", Namespace: "data"}}
	bundle, err := GenerateRuntimeMTLSBundle(instance, "MySQL sidecar")
	if err != nil {
		t.Fatal(err)
	}
	server := parseRuntimeCertificate(t, bundle.ServerCert)
	client := parseRuntimeCertificate(t, bundle.ClientCert)
	if len(server.ExtKeyUsage) != 1 || server.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatalf("server usages = %#v", server.ExtKeyUsage)
	}
	if !strings.Contains(strings.Join(server.DNSNames, ","), "*.orders.data.svc") {
		t.Fatalf("server DNS names = %#v", server.DNSNames)
	}
	if len(client.ExtKeyUsage) != 1 || client.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Fatalf("client usages = %#v", client.ExtKeyUsage)
	}
	if client.Subject.CommonName != "kdb-cluster-gateway" || len(client.DNSNames) != 0 {
		t.Fatalf("client identity = %q %#v", client.Subject.CommonName, client.DNSNames)
	}
	if string(bundle.ServerKey) == string(bundle.ClientKey) {
		t.Fatal("server and client must not share a private key")
	}
}

func parseRuntimeCertificate(t *testing.T, raw []byte) *x509.Certificate {
	t.Helper()
	block, _ := pem.Decode(raw)
	if block == nil {
		t.Fatal("invalid certificate PEM")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return certificate
}
