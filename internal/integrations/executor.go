package integrations

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/nabu-sh/nabu/internal/credentials"
)

const maximumResponseBytes = 2 * 1024 * 1024

var ErrApprovalRequired = errors.New("integrations: write operation requires explicit approval")

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Service struct {
	credentials credentials.Backend
	client      HTTPDoer
}

type ExecuteRequest struct {
	WorkspaceID   string
	IntegrationID string
	OperationID   string
	Approved      bool
}

type Result struct {
	OperationID string
	Kind        OperationKind
	StatusCode  int
	Body        []byte
}

// Destroy clears the in-memory provider response after its caller has consumed
// or handed it to a dataset ingestion boundary.
func (r *Result) Destroy() {
	if r == nil {
		return
	}
	wipe(r.Body)
	r.Body = nil
}

func NewService(backend credentials.Backend, client HTTPDoer) (*Service, error) {
	if backend == nil {
		return nil, errors.New("integrations: credential backend is required")
	}
	return &Service{credentials: backend, client: client}, nil
}

func (s *Service) Execute(ctx context.Context, manifest Manifest, request ExecuteRequest) (Result, error) {
	if ctx == nil {
		return Result{}, errors.New("integrations: nil context")
	}
	if err := manifest.Validate(); err != nil {
		return Result{}, err
	}
	if !manifestIdentifier.MatchString(request.WorkspaceID) {
		return Result{}, errors.New("integrations: invalid workspace ID")
	}
	// IntegrationID is the registry record ID used to scope credentials. It is
	// deliberately separate from Manifest.ID, which identifies the provider
	// definition and is not guaranteed to be unique within a workspace.
	if !manifestIdentifier.MatchString(request.IntegrationID) {
		return Result{}, errors.New("integrations: invalid integration registry ID")
	}
	operation, ok := findOperation(manifest, request.OperationID)
	if !ok {
		return Result{}, fmt.Errorf("integrations: operation %q not found", request.OperationID)
	}
	if operation.Kind == OperationWrite && !request.Approved {
		return Result{}, ErrApprovalRequired
	}

	base, _ := url.Parse(manifest.BaseURL)
	target := *base
	templateValues, templateLoaded, templateSensitive, err := s.loadTemplateValues(ctx, manifest, request.WorkspaceID, request.IntegrationID)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		for _, value := range templateLoaded {
			wipe(value)
		}
	}()
	rawPath, err := renderTemplate(operation.Path, templateValues, url.PathEscape)
	if err != nil {
		return Result{}, err
	}
	decodedPath, err := url.PathUnescape(rawPath)
	if err != nil {
		return Result{}, errors.New("integrations: render operation path")
	}
	target.Path = decodedPath
	target.RawPath = rawPath
	query := target.Query()
	for name, template := range operation.Query {
		value, renderErr := renderTemplate(template, templateValues, func(value string) string { return value })
		if renderErr != nil {
			return Result{}, renderErr
		}
		query.Set(name, value)
	}
	target.RawQuery = query.Encode()
	var body io.Reader
	if len(operation.RequestTemplate) > 0 {
		rendered, renderErr := renderJSONTemplate(operation.RequestTemplate, templateValues)
		if renderErr != nil {
			return Result{}, renderErr
		}
		defer wipe(rendered)
		body = bytes.NewReader(rendered)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, strings.ToUpper(operation.Method), target.String(), body)
	if err != nil {
		return Result{}, fmt.Errorf("integrations: create HTTP request: %w", err)
	}
	httpRequest.Header.Set("Accept", "application/json")
	httpRequest.Header.Set("User-Agent", "Nabu/0.1 integration")
	if body != nil {
		httpRequest.Header.Set("Content-Type", "application/json")
	}
	for name, template := range operation.Headers {
		value, renderErr := renderTemplate(template, templateValues, func(value string) string { return value })
		if renderErr != nil {
			return Result{}, renderErr
		}
		if strings.ContainsAny(value, "\x00\r\n") {
			return Result{}, fmt.Errorf("integrations: operation header %q contains an invalid control character", name)
		}
		httpRequest.Header.Set(name, value)
	}

	sensitive, err := s.applyAuth(ctx, manifest, request.WorkspaceID, request.IntegrationID, httpRequest)
	if err != nil {
		return Result{}, err
	}
	defer func() {
		clearAuth(httpRequest, manifest.Auth)
		httpRequest.URL.RawQuery = ""
		for name := range operation.Headers {
			httpRequest.Header.Del(name)
		}
		for _, value := range sensitive {
			wipe(value)
		}
	}()
	client := s.client
	if client == nil {
		client = restrictedClient(manifest)
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return Result{}, fmt.Errorf("integrations: execute %s operation: request failed", operation.Kind)
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maximumResponseBytes+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return Result{}, errors.New("integrations: read provider response")
	}
	if len(responseBody) > maximumResponseBytes {
		wipe(responseBody)
		return Result{}, fmt.Errorf("integrations: provider response exceeds %d bytes", maximumResponseBytes)
	}
	responseBody = redact(responseBody, append(sensitive, templateSensitive...))
	result := Result{OperationID: operation.ID, Kind: operation.Kind, StatusCode: response.StatusCode, Body: responseBody}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, fmt.Errorf("integrations: provider returned HTTP %d", response.StatusCode)
	}
	return result, nil
}

func (s *Service) loadTemplateValues(ctx context.Context, manifest Manifest, workspaceID, integrationID string) (map[string]string, [][]byte, [][]byte, error) {
	result := make(map[string]string)
	var loaded [][]byte
	var sensitive [][]byte
	for _, field := range manifest.RequiredFields() {
		secret, err := s.credentials.Get(ctx, credentials.Ref{WorkspaceID: workspaceID, Integration: integrationID, Name: field.Name})
		if errors.Is(err, credentials.ErrNotFound) && !field.Required {
			continue
		}
		if err != nil {
			for _, value := range loaded {
				wipe(value)
			}
			return nil, nil, nil, fmt.Errorf("integrations: connection field %q unavailable: %w", field.Name, err)
		}
		value, err := secret.Bytes()
		secret.Destroy()
		if err != nil {
			for _, prior := range loaded {
				wipe(prior)
			}
			return nil, nil, nil, err
		}
		if len(value) > 64*1024 || bytes.IndexByte(value, 0) >= 0 {
			wipe(value)
			for _, prior := range loaded {
				wipe(prior)
			}
			return nil, nil, nil, fmt.Errorf("integrations: connection field %q is invalid or too large", field.Name)
		}
		result[field.Name] = string(value)
		loaded = append(loaded, value)
		if field.Secret {
			sensitive = append(sensitive, value)
		}
	}
	return result, loaded, sensitive, nil
}

func renderTemplate(template string, values map[string]string, escape func(string) string) (string, error) {
	var renderErr error
	rendered := templateReference.ReplaceAllStringFunc(template, func(reference string) string {
		matches := templateReference.FindStringSubmatch(reference)
		value, exists := values[matches[1]]
		if !exists {
			renderErr = fmt.Errorf("integrations: connection field %q is unavailable", matches[1])
			return ""
		}
		return escape(value)
	})
	return rendered, renderErr
}

func renderJSONTemplate(raw json.RawMessage, values map[string]string) ([]byte, error) {
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, errors.New("integrations: decode request template")
	}
	var render func(any) (any, error)
	render = func(value any) (any, error) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				rendered, err := render(child)
				if err != nil {
					return nil, err
				}
				typed[key] = rendered
			}
			return typed, nil
		case []any:
			for index, child := range typed {
				rendered, err := render(child)
				if err != nil {
					return nil, err
				}
				typed[index] = rendered
			}
			return typed, nil
		case string:
			return renderTemplate(typed, values, func(value string) string { return value })
		default:
			return value, nil
		}
	}
	rendered, err := render(decoded)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(rendered)
	if err != nil {
		return nil, errors.New("integrations: encode request template")
	}
	return encoded, nil
}

func (s *Service) VerifyReadOnly(ctx context.Context, manifest Manifest, workspaceID, integrationID, operationID string) (Result, error) {
	operation, ok := findOperation(manifest, operationID)
	if !ok {
		return Result{}, fmt.Errorf("integrations: operation %q not found", operationID)
	}
	if operation.Kind != OperationRead {
		return Result{}, errors.New("integrations: verification operation must be read-only")
	}
	return s.Execute(ctx, manifest, ExecuteRequest{
		WorkspaceID: workspaceID, IntegrationID: integrationID, OperationID: operationID,
	})
}

func (s *Service) applyAuth(ctx context.Context, manifest Manifest, workspaceID, integrationID string, request *http.Request) ([][]byte, error) {
	ref := func(name string) credentials.Ref {
		return credentials.Ref{WorkspaceID: workspaceID, Integration: integrationID, Name: name}
	}
	get := func(name string) ([]byte, error) {
		secret, err := s.credentials.Get(ctx, ref(name))
		if err != nil {
			return nil, fmt.Errorf("integrations: credential %q unavailable: %w", name, err)
		}
		defer secret.Destroy()
		value, err := secret.Bytes()
		if err == nil && len(value) > 16*1024 {
			wipe(value)
			return nil, fmt.Errorf("integrations: credential %q exceeds the HTTP header limit", name)
		}
		if err == nil && bytes.IndexAny(value, "\x00\r\n") >= 0 {
			wipe(value)
			return nil, fmt.Errorf("integrations: credential %q is not valid for an HTTP header", name)
		}
		return value, err
	}
	var sensitive [][]byte
	switch manifest.Auth.Type {
	case AuthNone:
		return nil, nil
	case AuthBearer:
		value, err := get(manifest.Auth.Credential)
		if err != nil {
			return nil, err
		}
		sensitive = append(sensitive, value)
		request.Header.Set("Authorization", "Bearer "+string(value))
	case AuthHeader:
		value, err := get(manifest.Auth.Credential)
		if err != nil {
			return nil, err
		}
		sensitive = append(sensitive, value)
		request.Header.Set(manifest.Auth.Header, string(value))
	case AuthBasic:
		username, err := get(manifest.Auth.UsernameCredential)
		if err != nil {
			return nil, err
		}
		sensitive = append(sensitive, username)
		if bytes.IndexByte(username, ':') >= 0 {
			wipe(username)
			return nil, errors.New("integrations: basic auth username cannot contain a colon")
		}
		password, err := get(manifest.Auth.PasswordCredential)
		if err != nil {
			wipe(username)
			return nil, err
		}
		sensitive = append(sensitive, password)
		plain := make([]byte, 0, len(username)+len(password)+1)
		plain = append(plain, username...)
		plain = append(plain, ':')
		plain = append(plain, password...)
		encoded := make([]byte, base64.StdEncoding.EncodedLen(len(plain)))
		base64.StdEncoding.Encode(encoded, plain)
		wipe(plain)
		sensitive = append(sensitive, encoded)
		request.Header.Set("Authorization", "Basic "+string(encoded))
	default:
		return nil, fmt.Errorf("integrations: unsupported auth type %q", manifest.Auth.Type)
	}
	return sensitive, nil
}

func findOperation(manifest Manifest, id string) (Operation, bool) {
	for _, operation := range manifest.Operations {
		if operation.ID == id {
			return operation, true
		}
	}
	return Operation{}, false
}

func clearAuth(request *http.Request, auth Auth) {
	request.Header.Del("Authorization")
	if auth.Type == AuthHeader {
		request.Header.Del(auth.Header)
	}
}

func restrictedClient(manifest Manifest) *http.Client {
	base, _ := url.Parse(manifest.BaseURL)
	allowedHost := strings.ToLower(base.Hostname())
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil || strings.ToLower(host) != allowedHost {
			return nil, errors.New("integrations: connection target is not allowlisted")
		}
		addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil || len(addresses) == 0 {
			return nil, errors.New("integrations: resolve provider host")
		}
		for _, candidate := range addresses {
			if isPublicIP(candidate.IP) || (manifest.AllowLocalhost && candidate.IP.IsLoopback()) {
				dialer := net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}
				return dialer.DialContext(ctx, network, net.JoinHostPort(candidate.IP.String(), port))
			}
		}
		return nil, errors.New("integrations: provider resolved to a private or special-purpose address")
	}
	return &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("integrations: redirects are not allowed")
		},
	}
}

func redact(value []byte, sensitive [][]byte) []byte {
	redacted := append([]byte(nil), value...)
	for _, secret := range sensitive {
		if len(secret) > 0 {
			redacted = bytes.ReplaceAll(redacted, secret, []byte("[REDACTED]"))
		}
	}
	wipe(value)
	return redacted
}

func wipe(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
