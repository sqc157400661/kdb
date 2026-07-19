package postgresqlruntime

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"time"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
	"github.com/sqc157400661/kdb/internal/naming"
	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Client builds the single authenticated transport used by Operator callers
// of the native kdb-ha API. Secrets are read for every reconcile so credential
// and certificate rotation takes effect without restarting the Operator.
func Client(ctx context.Context, reader client.Reader, instance *v1.KDBInstance, timeout time.Duration) (*http.Client, string, string, error) {
	tlsSecret := &corev1.Secret{}
	meta := naming.PostgreSQLTLSSecret(instance)
	if err := reader.Get(ctx, client.ObjectKey{Namespace: meta.Namespace, Name: meta.Name}, tlsSecret); err != nil {
		return nil, "", "", err
	}
	ca := x509.NewCertPool()
	if !ca.AppendCertsFromPEM(tlsSecret.Data[naming.PostgreSQLTLSCAKey]) {
		return nil, "", "", fmt.Errorf("invalid PostgreSQL TLS CA")
	}
	certificate, err := tls.X509KeyPair(tlsSecret.Data[naming.PostgreSQLTLSClientCertKey], tlsSecret.Data[naming.PostgreSQLTLSClientPrivateKey])
	if err != nil {
		return nil, "", "", err
	}
	credentials := &corev1.Secret{}
	ref := naming.PostgreSQLCredentialSecret(instance)
	if instance.Spec.PostgreSQL != nil && instance.Spec.PostgreSQL.CredentialSecretRef != nil && instance.Spec.PostgreSQL.CredentialSecretRef.Name != "" {
		ref.Name = instance.Spec.PostgreSQL.CredentialSecretRef.Name
	}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, credentials); err != nil {
		return nil, "", "", err
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: ca, Certificates: []tls.Certificate{certificate}}
	return &http.Client{Timeout: timeout, Transport: transport}, string(credentials.Data[naming.PostgreSQLRESTAPIUsernameKey]), string(credentials.Data[naming.PostgreSQLRESTAPIPasswordKey]), nil
}

func PodEndpoint(instance *v1.KDBInstance, podName string) string {
	return fmt.Sprintf("https://%s.%s.%s.svc:8008", podName, naming.InstancePodServiceName(instance.Name), instance.Namespace)
}
