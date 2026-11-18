package actions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"rayxo/agent/internal/rpc"
	"rayxo/agent/internal/validate"
	"rayxo/agent/internal/webserver"
)

// maxNameservers bounds the nameservers ssl.issue checks a wildcard
// domain against.
const maxNameservers = 4

// issueCertificateParams are the params of ssl.issue. With wildcard, the
// certificate also covers *.<domain> and is issued through DNS-01, which
// needs the domain's nameservers to be among nameservers (this server's).
type issueCertificateParams struct {
	Username    string   `json:"username"`
	Domain      string   `json:"domain"`
	Wildcard    bool     `json:"wildcard"`
	Nameservers []string `json:"nameservers"`
}

func (p issueCertificateParams) account() string { return p.Username }

func (p issueCertificateParams) validate() error {
	if err := firstError(validate.Username(p.Username), validate.Domain(p.Domain)); err != nil {
		return err
	}

	if !p.Wildcard {
		if len(p.Nameservers) > 0 {
			return errors.New("nameservers only go with wildcard")
		}

		return nil
	}

	if len(p.Nameservers) == 0 || len(p.Nameservers) > maxNameservers {
		return fmt.Errorf("a wildcard certificate needs 1 to %d nameservers", maxNameservers)
	}

	for i, nameserver := range p.Nameservers {
		if err := validate.Domain(nameserver); err != nil {
			return fmt.Errorf("nameserver: %w", err)
		}

		if slices.Contains(p.Nameservers[:i], nameserver) {
			return fmt.Errorf("nameserver %q is given twice", nameserver)
		}
	}

	return nil
}

// certificateInfo is the result of ssl.issue.
type certificateInfo struct {
	Names     []string `json:"names"`
	ExpiresAt string   `json:"expires_at"`
	// WildcardFallback is true when a wildcard certificate was asked for but
	// the domain does not use this server's nameservers, so a standard
	// certificate (the domain and www) was issued instead.
	WildcardFallback bool `json:"wildcard_fallback,omitempty"`
}

// issueCertificate implements ssl.issue: it gets a Let's Encrypt
// certificate for a domain of the account, and for www.<domain> when that
// reaches this server too; with wildcard, for the domain and *.<domain>
// through DNS-01 instead. A wildcard whose domain does not use this server's
// nameservers cannot be issued through DNS-01 (e.g. a domain on Cloudflare's
// nameservers), so a standard certificate is issued instead and the result
// reports it (WildcardFallback). A valid certificate for the same names is
// kept, so calling it again returns the same result. It does not turn HTTPS
// on; web.apply_domain with ssl does.
func (w webActions) issueCertificate(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[issueCertificateParams](raw)
	if err != nil {
		return nil, err
	}

	user, err := w.checkDomain(ctx, params.Username, params.Domain)
	if err != nil {
		return nil, err
	}

	certificate, fallback, err := w.issueFor(ctx, user.Name, params)
	if err != nil {
		return nil, webError(err, params.Domain)
	}

	return certificateInfo{
		Names:            certificate.Names,
		ExpiresAt:        certificate.ExpiresAt.UTC().Format(time.RFC3339),
		WildcardFallback: fallback,
	}, nil
}

// issueFor issues the certificate ssl.issue asks for. A wildcard request
// whose domain does not use this server's nameservers (*NotDelegatedError)
// cannot get a DNS-01 certificate, so it falls back to a standard HTTP-01
// certificate for the domain and www; fallback reports that this happened.
func (w webActions) issueFor(ctx context.Context, user string, params issueCertificateParams) (cert webserver.Certificate, fallback bool, err error) {
	if !params.Wildcard {
		cert, err = w.web.IssueCertificate(ctx, user, params.Domain)

		return cert, false, err
	}

	cert, err = w.web.IssueWildcardCertificate(ctx, user, params.Domain, params.Nameservers)
	if _, notDelegated := errors.AsType[*webserver.NotDelegatedError](err); notDelegated {
		cert, err = w.web.IssueCertificate(ctx, user, params.Domain)

		return cert, true, err
	}

	return cert, false, err
}

// maxExpiryDomains bounds the domains one ssl.expiry call reads.
const maxExpiryDomains = 500

// expiryParams are the params of ssl.expiry.
type expiryParams struct {
	Domains []string `json:"domains"`
}

func (p expiryParams) validate() error {
	if len(p.Domains) == 0 || len(p.Domains) > maxExpiryDomains {
		return fmt.Errorf("domains needs 1 to %d domains", maxExpiryDomains)
	}

	seen := make(map[string]bool, len(p.Domains))

	for _, domain := range p.Domains {
		if err := validate.Domain(domain); err != nil {
			return err
		}

		if seen[domain] {
			return fmt.Errorf("domain %q is given twice", domain)
		}

		seen[domain] = true
	}

	return nil
}

// expiryInfo is the result of ssl.expiry: when the certificate of each
// domain that has one expires, RFC 3339 in UTC.
type expiryInfo struct {
	ExpiresAt map[string]string `json:"expires_at"`
}

// reportExpiry implements ssl.expiry: when the Let's Encrypt certificates
// of domains expire, as they are on disk now, so the panel learns of the
// renewals certbot makes on its own. A domain without a certificate is
// left out. It only reads, so it locks nothing.
func (w webActions) reportExpiry(_ context.Context, raw json.RawMessage) (any, error) {
	var params expiryParams
	if err := rpc.DecodeParams(raw, &params); err != nil {
		return nil, err
	}

	if err := params.validate(); err != nil {
		return nil, rpc.Errorf(rpc.CodeInvalidParams, "invalid params: %v", err)
	}

	info := expiryInfo{ExpiresAt: make(map[string]string, len(params.Domains))}

	for _, domain := range params.Domains {
		if expiresAt, ok := w.web.CertificateExpiry(domain); ok {
			info.ExpiresAt[domain] = expiresAt.UTC().Format(time.RFC3339)
		}
	}

	return info, nil
}

// installCertificateParams are the params of ssl.install. Certificate and
// PrivateKey are PEM; the key never reaches a log.
type installCertificateParams struct {
	Username    string `json:"username"`
	Domain      string `json:"domain"`
	Certificate string `json:"certificate"`
	PrivateKey  string `json:"private_key"`
}

func (p installCertificateParams) account() string { return p.Username }

func (p installCertificateParams) validate() error {
	if p.Certificate == "" || p.PrivateKey == "" {
		return errors.New("certificate and private_key are required")
	}

	return firstError(validate.Username(p.Username), validate.Domain(p.Domain))
}

// customCertificateInfo is the result of ssl.install.
type customCertificateInfo struct {
	Names     []string `json:"names"`
	ExpiresAt string   `json:"expires_at"`
	Issuer    string   `json:"issuer"`
	Trusted   bool     `json:"trusted"`
}

// installCertificate implements ssl.install: it checks a certificate the
// customer brings (with its intermediates) and its private key for a
// domain of the account, and installs them. It does not turn HTTPS on;
// web.apply_domain with ssl and certificate "custom" does. Installing again
// replaces the certificate.
func (w webActions) installCertificate(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[installCertificateParams](raw)
	if err != nil {
		return nil, err
	}

	user, err := w.checkDomain(ctx, params.Username, params.Domain)
	if err != nil {
		return nil, err
	}

	certificate, err := w.web.InstallCertificate(ctx, user.Name, params.Domain, params.Certificate, params.PrivateKey)
	if err != nil {
		return nil, webError(err, params.Domain)
	}

	return customCertificateInfo{
		Names:     certificate.Names,
		ExpiresAt: certificate.ExpiresAt.UTC().Format(time.RFC3339),
		Issuer:    certificate.Issuer,
		Trusted:   certificate.Trusted,
	}, nil
}

// deleteCertificate implements ssl.delete: it deletes the certificate of a
// domain of the account once its HTTPS is off. A domain without a
// certificate succeeds.
func (w webActions) deleteCertificate(ctx context.Context, raw json.RawMessage) (any, error) {
	params, err := decodeWebParams[domainParams](raw)
	if err != nil {
		return nil, err
	}

	user, err := w.checkDomain(ctx, params.Username, params.Domain)
	if err != nil {
		return nil, err
	}

	if err := w.web.DeleteCertificate(ctx, user.Name, params.Domain); err != nil {
		return nil, webError(err, params.Domain)
	}

	return struct{}{}, nil
}
