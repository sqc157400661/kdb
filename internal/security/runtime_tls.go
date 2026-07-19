package security

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"time"

	v1 "github.com/sqc157400661/kdb/apis/kdb.com/v1"
)

type RuntimeMTLSBundle struct {
	CA         []byte
	ServerCert []byte
	ServerKey  []byte
	ClientCert []byte
	ClientKey  []byte
}

func GenerateRuntimeTLSBundle(instance *v1.KDBInstance, purpose string) ([]byte, []byte, []byte, error) {
	bundle, err := GenerateRuntimeMTLSBundle(instance, purpose)
	if err != nil {
		return nil, nil, nil, err
	}
	return bundle.CA, bundle.ServerCert, bundle.ServerKey, nil
}

func GenerateRuntimeMTLSBundle(instance *v1.KDBInstance, purpose string) (RuntimeMTLSBundle, error) {
	return GenerateRuntimeMTLSBundleWithDNS(instance, purpose, nil)
}

func GenerateRuntimeMTLSBundleWithDNS(instance *v1.KDBInstance, purpose string, extraDNS []string) (RuntimeMTLSBundle, error) {
	now := time.Now().UTC()
	limit := new(big.Int).Lsh(big.NewInt(1), 128)
	caSerial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return RuntimeMTLSBundle{}, err
	}
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return RuntimeMTLSBundle{}, err
	}
	caTemplate := &x509.Certificate{
		SerialNumber: caSerial, Subject: pkix.Name{CommonName: instance.Name + " " + purpose + " runtime CA"},
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(3, 0, 0),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		return RuntimeMTLSBundle{}, err
	}
	serverDNS := append(dnsNames(instance), extraDNS...)
	serverCert, serverKey, err := generateRuntimeLeaf(instance.Name, serverDNS, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, caTemplate, caKey, now, limit)
	if err != nil {
		return RuntimeMTLSBundle{}, err
	}
	clientCert, clientKey, err := generateRuntimeLeaf(
		"kdb-cluster-gateway", nil, []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, caTemplate, caKey, now, limit,
	)
	if err != nil {
		return RuntimeMTLSBundle{}, err
	}
	return RuntimeMTLSBundle{
		CA:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}),
		ServerCert: serverCert,
		ServerKey:  serverKey,
		ClientCert: clientCert,
		ClientKey:  clientKey,
	}, nil
}

func generateRuntimeLeaf(commonName string, dns []string, usages []x509.ExtKeyUsage, caTemplate *x509.Certificate, caKey *rsa.PrivateKey, now time.Time, limit *big.Int) ([]byte, []byte, error) {
	serial, err := rand.Int(rand.Reader, limit)
	if err != nil {
		return nil, nil, err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: commonName}, DNSNames: dns,
		NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(1, 0, 0),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: usages,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, caTemplate, &key.PublicKey, caKey)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), nil
}

func dnsNames(instance *v1.KDBInstance) []string {
	return []string{
		instance.Name, instance.Name + "." + instance.Namespace,
		instance.Name + "." + instance.Namespace + ".svc",
		"*." + instance.Name + "." + instance.Namespace + ".svc", "localhost",
	}
}
