package signer

import (
	"context"
	"errors"
	"fmt"
	"os"

	"golang.org/x/oauth2/google"
	goauth2 "google.golang.org/api/oauth2/v1"
)

const impSaEnvName = "CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT"

// All patterns are defined in
// https://github.com/golang/oauth2/blob/f6687ab2804cbebdfdeef385bee94918b1ce83de/google/google.go#L93-L98
const (
	serviceAccountKey  = "service_account"
	userCredentialsKey = "authorized_user"
	externalAccountKey = "external_account"
)

type smartSignerConfig struct {
	targetPrincipal string
	delegates       []string
}

type Option func(*smartSignerConfig) error

func WithTargetPrincipal(targetPrincipal string) Option {
	return func(config *smartSignerConfig) error {
		config.targetPrincipal = targetPrincipal
		return nil
	}
}

func WithDelegates(delegates ...string) Option {
	return func(config *smartSignerConfig) error {
		config.delegates = delegates
		return nil
	}
}

func calcSmartSignerConfig(options ...Option) (*smartSignerConfig, error) {
	var config smartSignerConfig
	for _, option := range options {
		if err := option(&config); err != nil {
			return nil, err
		}
	}

	if config.targetPrincipal == "" && len(config.delegates) > 0 {
		return nil, fmt.Errorf("targetPrincipal is set but delegates is not set: %s", config.delegates)
	}
	if impSaVal := os.Getenv(impSaEnvName); config.targetPrincipal == "" && impSaVal != "" {
		config.targetPrincipal, config.delegates = parseDelegateChain(impSaVal)
	}
	return &config, nil
}

// SmartSigner create signer for ADC with optional impersonation.
// Impersonation setting is supplied from below in descending order of priority.
// 	1. options e.g. signer.WithTargetPrincipal, signer.WithDelegates
// 	2. `CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT` environment variable
func SmartSigner(ctx context.Context, options ...Option) (Signer, error) {
	config, err := calcSmartSignerConfig(options...)
	if err != nil {
		return nil, err
	}

	if config.targetPrincipal != "" {
		return &iamCredentialsSigner{
			target:    config.targetPrincipal,
			delegates: config.delegates,
		}, nil
	}

	credential, err := google.FindDefaultCredentials(ctx)
	if err != nil {
		return nil, err
	}

	if len(credential.JSON) != 0 {
		t, err := credentialType(credential.JSON)
		if err != nil {
			return nil, err
		}

		switch t {
		case userCredentialsKey:
			return nil, fmt.Errorf("authorized_user is unsupported so set CLOUDSDK_AUTH_IMPERSONATE_SERVICE_ACCOUNT or use other credential")
		case serviceAccountKey:
			return ServiceAccountSigner(credential.JSON)
		case externalAccountKey:
			fallthrough
		default:
			// fallthrough
		}
	}
	// Get email from tokeninfo of ADC
	oauth2Svc, err := goauth2.NewService(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := oauth2Svc.Tokeninfo().Do()
	if err != nil {
		return nil, err
	}

	if resp.Email == "" {
		return nil, errors.New("signer.SmartSigner can't infer email")
	}
	// Use itself as target
	return IamCredentialsSigner(resp.Email, nil, nil)
}