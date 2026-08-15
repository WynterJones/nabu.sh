// Package integrations defines a declarative, bounded HTTP integration
// boundary. Provider credentials are never passed to scripts or subprocesses.
package integrations

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const (
	maximumOperations      = 32
	maximumFields          = 16
	maximumQueryParameters = 32
	maximumHeaders         = 16
	maximumRequestBytes    = 64 * 1024
	maximumNameBytes       = 160
	maximumDescriptionSize = 2 * 1024
)

var manifestIdentifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var templateReference = regexp.MustCompile(`\{\{([A-Za-z0-9][A-Za-z0-9._-]{0,127})\}\}`)

type OperationKind string

const (
	OperationRead  OperationKind = "read"
	OperationWrite OperationKind = "write"
)

type AuthType string

const (
	AuthNone   AuthType = ""
	AuthBearer AuthType = "bearer"
	AuthHeader AuthType = "header"
	AuthBasic  AuthType = "basic"
)

type Manifest struct {
	ID             string      `json:"id"`
	Name           string      `json:"name"`
	Description    string      `json:"description,omitempty"`
	BaseURL        string      `json:"base_url"`
	AllowedHosts   []string    `json:"allowed_hosts"`
	AllowLocalhost bool        `json:"allow_localhost,omitempty"`
	Auth           Auth        `json:"auth,omitempty"`
	Fields         []Field     `json:"fields,omitempty"`
	Operations     []Operation `json:"operations"`
}

// Field declares one connection value required by a generated adapter. Values
// are stored in the platform credential store regardless of whether they are
// secret; only this descriptive schema is persisted with the manifest.
type Field struct {
	Name        string `json:"name"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Secret      bool   `json:"secret"`
	Required    bool   `json:"required"`
}

type Auth struct {
	Type               AuthType `json:"type,omitempty"`
	Credential         string   `json:"credential,omitempty"`
	Header             string   `json:"header,omitempty"`
	UsernameCredential string   `json:"username_credential,omitempty"`
	PasswordCredential string   `json:"password_credential,omitempty"`
}

type Operation struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Method          string            `json:"method"`
	Path            string            `json:"path"`
	Kind            OperationKind     `json:"kind"`
	Query           map[string]string `json:"query,omitempty"`
	Headers         map[string]string `json:"headers,omitempty"`
	RequestTemplate json.RawMessage   `json:"request_template,omitempty"`
}

// RequiredFields returns a stable, de-duplicated setup schema. Older
// manifests that only declare auth remain compatible through implicit secret
// fields derived from their auth references.
func (m Manifest) RequiredFields() []Field {
	result := append([]Field(nil), m.Fields...)
	seen := make(map[string]struct{}, len(result))
	for _, field := range result {
		seen[field.Name] = struct{}{}
	}
	for _, name := range authCredentialNames(m.Auth) {
		if _, exists := seen[name]; exists {
			continue
		}
		result = append(result, Field{
			Name: name, Label: humanizeIdentifier(name),
			Description: "Stored securely in the platform credential store.",
			Secret:      true, Required: true,
		})
		seen[name] = struct{}{}
	}
	return result
}

func ParseManifest(raw []byte) (Manifest, error) {
	if len(raw) == 0 || len(raw) > 256*1024 {
		return Manifest{}, errors.New("integrations: manifest must be between 1 byte and 256 KiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("integrations: decode manifest: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Manifest{}, errors.New("integrations: manifest must contain one JSON object")
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func (m Manifest) Validate() error {
	if !manifestIdentifier.MatchString(m.ID) {
		return errors.New("integrations: invalid manifest ID")
	}
	if strings.TrimSpace(m.Name) == "" || len(m.Name) > maximumNameBytes {
		return errors.New("integrations: manifest name is required and must not exceed 160 bytes")
	}
	if len(m.Description) > maximumDescriptionSize {
		return errors.New("integrations: manifest description exceeds 2 KiB")
	}
	base, err := validateBaseURL(m.BaseURL, m.AllowLocalhost)
	if err != nil {
		return err
	}
	hosts, err := validateHosts(m.AllowedHosts, m.AllowLocalhost)
	if err != nil {
		return err
	}
	if _, allowed := hosts[strings.ToLower(base.Hostname())]; !allowed {
		return fmt.Errorf("integrations: base URL host %q is not allowlisted", base.Hostname())
	}
	if err := validateAuth(m.Auth); err != nil {
		return err
	}
	fields, err := validateFields(m.Fields, m.Auth)
	if err != nil {
		return err
	}
	if len(m.Operations) == 0 || len(m.Operations) > maximumOperations {
		return fmt.Errorf("integrations: manifest requires 1-%d operations", maximumOperations)
	}
	seen := make(map[string]struct{}, len(m.Operations))
	for index, operation := range m.Operations {
		if err := validateOperation(operation, fields); err != nil {
			return fmt.Errorf("integrations: operation %d: %w", index+1, err)
		}
		if _, exists := seen[operation.ID]; exists {
			return fmt.Errorf("integrations: duplicate operation ID %q", operation.ID)
		}
		seen[operation.ID] = struct{}{}
	}
	return nil
}

func validateFields(values []Field, auth Auth) (map[string]Field, error) {
	if len(values) > maximumFields {
		return nil, fmt.Errorf("integrations: manifest fields exceed %d", maximumFields)
	}
	fields := make(map[string]Field, len(values))
	for index, field := range values {
		field.Name = strings.TrimSpace(field.Name)
		field.Label = strings.TrimSpace(field.Label)
		field.Description = strings.TrimSpace(field.Description)
		if !manifestIdentifier.MatchString(field.Name) {
			return nil, fmt.Errorf("integrations: field %d has an invalid name", index+1)
		}
		if field.Label == "" || len(field.Label) > maximumNameBytes {
			return nil, fmt.Errorf("integrations: field %q requires a label of at most %d bytes", field.Name, maximumNameBytes)
		}
		if len(field.Description) > maximumDescriptionSize {
			return nil, fmt.Errorf("integrations: field %q description exceeds 2 KiB", field.Name)
		}
		if _, exists := fields[field.Name]; exists {
			return nil, fmt.Errorf("integrations: duplicate field %q", field.Name)
		}
		fields[field.Name] = field
	}
	for _, name := range authCredentialNames(auth) {
		field, exists := fields[name]
		if exists && (!field.Secret || !field.Required) {
			return nil, fmt.Errorf("integrations: auth field %q must be required and secret", name)
		}
		if !exists {
			fields[name] = Field{Name: name, Label: humanizeIdentifier(name), Secret: true, Required: true}
		}
	}
	return fields, nil
}

func authCredentialNames(auth Auth) []string {
	switch auth.Type {
	case AuthBearer, AuthHeader:
		return []string{auth.Credential}
	case AuthBasic:
		return []string{auth.UsernameCredential, auth.PasswordCredential}
	default:
		return nil
	}
}

func humanizeIdentifier(value string) string {
	words := strings.Fields(strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(value))
	for index := range words {
		if len(words[index]) > 0 {
			words[index] = strings.ToUpper(words[index][:1]) + words[index][1:]
		}
	}
	return strings.Join(words, " ")
}

func validateBaseURL(value string, allowLocalhost bool) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return nil, errors.New("integrations: base_url must be an absolute URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") {
		return nil, errors.New("integrations: base_url must contain only scheme and host")
	}
	local := isLocalHost(parsed.Hostname())
	if parsed.Scheme != "https" && !(allowLocalhost && local && parsed.Scheme == "http") {
		return nil, errors.New("integrations: base_url must use HTTPS; HTTP is allowed only for an explicit localhost integration")
	}
	if local && !allowLocalhost {
		return nil, errors.New("integrations: localhost requires allow_localhost")
	}
	if ip := net.ParseIP(parsed.Hostname()); ip != nil && !isPublicIP(ip) && !(allowLocalhost && ip.IsLoopback()) {
		return nil, errors.New("integrations: private and special-purpose IP hosts are not allowed")
	}
	return parsed, nil
}

func validateHosts(values []string, allowLocalhost bool) (map[string]struct{}, error) {
	if len(values) == 0 || len(values) > 16 {
		return nil, errors.New("integrations: allowed_hosts requires 1-16 exact hosts")
	}
	hosts := make(map[string]struct{}, len(values))
	for _, raw := range values {
		host := strings.ToLower(strings.TrimSpace(raw))
		if host == "" || strings.ContainsAny(host, "*/:@[] ") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
			return nil, fmt.Errorf("integrations: invalid allowed host %q", raw)
		}
		if isLocalHost(host) && !allowLocalhost {
			return nil, errors.New("integrations: localhost host requires allow_localhost")
		}
		if ip := net.ParseIP(host); ip != nil && !isPublicIP(ip) && !(allowLocalhost && ip.IsLoopback()) {
			return nil, fmt.Errorf("integrations: private or special-purpose host %q is not allowed", raw)
		}
		hosts[host] = struct{}{}
	}
	return hosts, nil
}

func validateAuth(auth Auth) error {
	validCredential := func(value string) bool { return manifestIdentifier.MatchString(value) }
	switch auth.Type {
	case AuthNone:
		if auth.Credential != "" || auth.Header != "" || auth.UsernameCredential != "" || auth.PasswordCredential != "" {
			return errors.New("integrations: auth fields require an auth type")
		}
	case AuthBearer:
		if !validCredential(auth.Credential) || auth.Header != "" || auth.UsernameCredential != "" || auth.PasswordCredential != "" {
			return errors.New("integrations: bearer auth requires only credential")
		}
	case AuthHeader:
		if !validCredential(auth.Credential) || !validAuthHeader(auth.Header) || auth.UsernameCredential != "" || auth.PasswordCredential != "" {
			return errors.New("integrations: header auth requires a safe header and credential")
		}
	case AuthBasic:
		if !validCredential(auth.UsernameCredential) || !validCredential(auth.PasswordCredential) || auth.Credential != "" || auth.Header != "" {
			return errors.New("integrations: basic auth requires username_credential and password_credential")
		}
	default:
		return fmt.Errorf("integrations: unsupported auth type %q; OAuth and request signing require a dedicated audited adapter", auth.Type)
	}
	return nil
}

func validAuthHeader(value string) bool {
	if !regexp.MustCompile(`^[A-Za-z][A-Za-z0-9-]{0,63}$`).MatchString(value) {
		return false
	}
	switch strings.ToLower(value) {
	case "authorization", "host", "content-length", "connection", "transfer-encoding", "cookie", "set-cookie":
		return false
	default:
		return true
	}
}

func validateOperation(operation Operation, fields map[string]Field) error {
	if !manifestIdentifier.MatchString(operation.ID) {
		return errors.New("invalid operation ID")
	}
	if strings.TrimSpace(operation.Name) == "" || len(operation.Name) > maximumNameBytes {
		return errors.New("operation name is required and must not exceed 160 bytes")
	}
	method := strings.ToUpper(strings.TrimSpace(operation.Method))
	switch operation.Kind {
	case OperationRead:
		if method != http.MethodGet && method != http.MethodHead && method != http.MethodPost {
			return errors.New("read operations must use GET, HEAD, or a documented POST query")
		}
		if method == http.MethodPost && len(operation.RequestTemplate) == 0 {
			return errors.New("POST read operations require a bounded JSON query template")
		}
		if method != http.MethodPost && len(operation.RequestTemplate) > 0 {
			return errors.New("GET and HEAD read operations cannot have a request template")
		}
	case OperationWrite:
		switch method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			return errors.New("write operations must use POST, PUT, PATCH, or DELETE")
		}
	default:
		return errors.New("operation kind must be read or write")
	}
	pathProbe := templateReference.ReplaceAllString(operation.Path, "field-value")
	parsed, err := url.ParseRequestURI(pathProbe)
	if err != nil || !strings.HasPrefix(operation.Path, "/") || strings.HasPrefix(operation.Path, "//") ||
		parsed.Host != "" || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(parsed.Path, "..") ||
		strings.Contains(operation.Path, "%") {
		return errors.New("operation path must be an absolute static path without host, query, fragment, or traversal")
	}
	if err := validateTemplate(operation.Path, fields, false); err != nil {
		return fmt.Errorf("operation path: %w", err)
	}
	if len(operation.RequestTemplate) > maximumRequestBytes {
		return fmt.Errorf("request template exceeds %d bytes", maximumRequestBytes)
	}
	if len(operation.RequestTemplate) > 0 && !json.Valid(operation.RequestTemplate) {
		return errors.New("request template must be valid JSON")
	}
	if len(operation.Query) > maximumQueryParameters {
		return fmt.Errorf("operation query exceeds %d parameters", maximumQueryParameters)
	}
	for name, value := range operation.Query {
		if !manifestIdentifier.MatchString(name) || len(value) > 4*1024 {
			return fmt.Errorf("operation query parameter %q is invalid or too large", name)
		}
		if err := validateTemplate(value, fields, true); err != nil {
			return fmt.Errorf("operation query parameter %q: %w", name, err)
		}
	}
	if len(operation.Headers) > maximumHeaders {
		return fmt.Errorf("operation headers exceed %d", maximumHeaders)
	}
	for name, value := range operation.Headers {
		if !validAuthHeader(name) || len(value) > 16*1024 {
			return fmt.Errorf("operation header %q is invalid or too large", name)
		}
		if err := validateTemplate(value, fields, true); err != nil {
			return fmt.Errorf("operation header %q: %w", name, err)
		}
	}
	if len(operation.RequestTemplate) > 0 {
		var body any
		if err := json.Unmarshal(operation.RequestTemplate, &body); err != nil {
			return errors.New("request template must be valid JSON")
		}
		if err := validateJSONTemplates(body, fields); err != nil {
			return fmt.Errorf("request template: %w", err)
		}
	}
	return nil
}

func validateJSONTemplates(value any, fields map[string]Field) error {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if strings.Contains(key, "{{") {
				return errors.New("field references are not allowed in JSON object keys")
			}
			if err := validateJSONTemplates(child, fields); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range typed {
			if err := validateJSONTemplates(child, fields); err != nil {
				return err
			}
		}
	case string:
		return validateTemplate(typed, fields, true)
	}
	return nil
}

func validateTemplate(value string, fields map[string]Field, allowSecret bool) error {
	for _, match := range templateReference.FindAllStringSubmatch(value, -1) {
		field, exists := fields[match[1]]
		if !exists {
			return fmt.Errorf("unknown field reference %q", match[1])
		}
		if field.Secret && !allowSecret {
			return fmt.Errorf("secret field %q may only be used by the auth configuration", match[1])
		}
	}
	withoutReferences := templateReference.ReplaceAllString(value, "")
	if strings.Contains(withoutReferences, "{{") || strings.Contains(withoutReferences, "}}") {
		return errors.New("malformed field reference")
	}
	return nil
}

func isLocalHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func isPublicIP(ip net.IP) bool {
	return ip != nil && !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsUnspecified() && !ip.IsMulticast() && !ip.IsLinkLocalUnicast() && !ip.IsLinkLocalMulticast()
}
