package apiclient

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"math"
	"time"

	"github.com/argoproj/argo-cd/v3/common"
	"github.com/argoproj/argo-cd/v3/util/env"

	grpc_retry "github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/retry"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/timeout"
	log "github.com/sirupsen/logrus"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	grpc_util "github.com/argoproj/argo-cd/v3/util/grpc"
	utilio "github.com/argoproj/argo-cd/v3/util/io"
)

// MaxGRPCMessageSize contains max grpc message size
var MaxGRPCMessageSize = env.ParseNumFromEnv(common.EnvGRPCMaxSizeMB, 100, 0, math.MaxInt32) * 1024 * 1024

// TLSConfiguration describes parameters for TLS configuration to be used by a repo server API client
type TLSConfiguration struct {
	// Whether to disable TLS for connections
	DisableTLS bool
	// Whether to enforce strict validation of TLS certificates
	StrictValidation bool
	// List of certificates to validate the peer against (if StrictCerts is true)
	Certificates *x509.CertPool
}

// Clientset represents repository server api clients
type Clientset interface {
	NewRepoServerClient() (utilio.Closer, RepoServerServiceClient, error)
}

type clientSet struct {
	address        string
	timeoutSeconds int
	tlsConfig      TLSConfiguration
}

func (c *clientSet) NewRepoServerClient() (utilio.Closer, RepoServerServiceClient, error) {
	conn, err := NewConnection(c.address, c.timeoutSeconds, &c.tlsConfig)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to open a new connection to repo server: %w", err)
	}
	return conn, NewRepoServerServiceClient(conn), nil
}

func NewConnection(address string, timeoutSeconds int, tlsConfig *TLSConfiguration) (*grpc.ClientConn, error) {
	retryOpts := []grpc_retry.CallOption{
		grpc_retry.WithMax(3),
		grpc_retry.WithBackoff(grpc_retry.BackoffLinear(1000 * time.Millisecond)),
	}
	unaryInterceptors := []grpc.UnaryClientInterceptor{grpc_retry.UnaryClientInterceptor(retryOpts...)}
	if timeoutSeconds > 0 {
		unaryInterceptors = append(unaryInterceptors, timeout.UnaryClientInterceptor(time.Duration(timeoutSeconds)*time.Second))
	}
	opts := []grpc.DialOption{
		grpc.WithStreamInterceptor(grpc_util.RetryOnlyForServerStreamInterceptor(retryOpts...)),
		grpc.WithChainUnaryInterceptor(unaryInterceptors...),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(MaxGRPCMessageSize), grpc.MaxCallSendMsgSize(MaxGRPCMessageSize)),
		grpc.WithStatsHandler(otelgrpc.NewClientHandler()),
	}

	tlsC := &tls.Config{}
	if !tlsConfig.DisableTLS {
		if !tlsConfig.StrictValidation {
			tlsC.InsecureSkipVerify = true
		} else {
			tlsC.RootCAs = tlsConfig.Certificates
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsC)))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	//nolint:staticcheck
	conn, err := grpc.Dial(address, opts...)
	if err != nil {
		log.Errorf("Unable to connect to repository service with address %s", address)
		return nil, err
	}
	return conn, nil
}

// NewRepoServerClientset creates new instance of repo server Clientset
func NewRepoServerClientset(address string, timeoutSeconds int, tlsConfig TLSConfiguration) Clientset {
	return &clientSet{address: address, timeoutSeconds: timeoutSeconds, tlsConfig: tlsConfig}
}
