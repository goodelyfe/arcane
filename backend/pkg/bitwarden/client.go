// Package bitwarden is a small client for the Bitwarden CLI's Vault Management
// API (`bw serve`). The CLI logs in and unlocks the vault, so this works with
// Bitwarden and Vaultwarden alike without Arcane handling vault keys.
package bitwarden

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxResponseBytes = 10 << 20

// Item types and custom field types as defined by Bitwarden.
const (
	ItemTypeLogin      = 1
	ItemTypeSecureNote = 2

	FieldTypeText    = 0
	FieldTypeHidden  = 1
	FieldTypeBoolean = 2
	FieldTypeLinked  = 3
)

// ErrVaultLocked means `bw serve` is running but its vault is not unlocked.
var ErrVaultLocked = errors.New("the Bitwarden CLI vault is locked; unlock it in the bw serve container")

// APIError is an error reported by `bw serve`.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("bw serve returned HTTP %d", e.StatusCode)
	}
	return fmt.Sprintf("bw serve returned HTTP %d: %s", e.StatusCode, e.Message)
}

type Status struct {
	ServerURL string `json:"serverUrl"`
	UserEmail string `json:"userEmail"`
	Status    string `json:"status"`
}

type Folder struct {
	ID   *string `json:"id"`
	Name string  `json:"name"`
}

type Collection struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	OrganizationID string `json:"organizationId"`
}

type Login struct {
	Password *string `json:"password"`
}

type Field struct {
	Name  string  `json:"name"`
	Value *string `json:"value"`
	Type  int     `json:"type"`
}

type Item struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Type        int     `json:"type"`
	Notes       *string `json:"notes"`
	Login       *Login  `json:"login"`
	Fields      []Field `json:"fields"`
	FolderID    *string `json:"folderId"`
	DeletedDate *string `json:"deletedDate"`
}

// ItemFilter narrows ListItems to one folder or one collection.
type ItemFilter struct {
	FolderID     string
	CollectionID string
}

// Client talks to one `bw serve` endpoint. It is safe for concurrent use.
type Client struct {
	httpClient *http.Client
	baseURL    *url.URL
}

// NewClient validates serveURL and returns a client. httpClient may be nil.
func NewClient(httpClient *http.Client, serveURL string) (*Client, error) {
	baseURL, err := ParseServeURL(serveURL)
	if err != nil {
		return nil, err
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{httpClient: httpClient, baseURL: baseURL}, nil
}

// ParseServeURL validates the address of a `bw serve` endpoint, such as
// http://bw-serve:8087.
func ParseServeURL(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("the bw serve URL is required, e.g. http://bw-serve:8087")
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid bw serve URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("invalid bw serve URL: scheme must be http or https, got %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return nil, errors.New("invalid bw serve URL: missing host")
	}
	if parsed.User != nil {
		return nil, errors.New("invalid bw serve URL: credentials in the URL are not allowed")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed, nil
}

// Status reports which account the CLI is signed in to and whether it is
// unlocked. It works while the vault is locked.
func (c *Client) Status(ctx context.Context) (Status, error) {
	var data struct {
		Template Status `json:"template"`
	}
	if err := c.doInternal(ctx, http.MethodGet, "/status", nil, &data); err != nil {
		return Status{}, err
	}
	return data.Template, nil
}

// Sync pulls the latest vault contents from the server into the CLI.
func (c *Client) Sync(ctx context.Context) error {
	return c.doInternal(ctx, http.MethodPost, "/sync", nil, nil)
}

func (c *Client) ListFolders(ctx context.Context) ([]Folder, error) {
	var list listEnvelope[Folder]
	if err := c.doInternal(ctx, http.MethodGet, "/list/object/folders", nil, &list); err != nil {
		return nil, err
	}
	return list.Data, nil
}

func (c *Client) ListCollections(ctx context.Context) ([]Collection, error) {
	var list listEnvelope[Collection]
	if err := c.doInternal(ctx, http.MethodGet, "/list/object/collections", nil, &list); err != nil {
		return nil, err
	}
	return list.Data, nil
}

// ListItems lists items, excluding ones in the trash.
func (c *Client) ListItems(ctx context.Context, filter ItemFilter) ([]Item, error) {
	query := url.Values{}
	if filter.FolderID != "" {
		query.Set("folderid", filter.FolderID)
	}
	if filter.CollectionID != "" {
		query.Set("collectionid", filter.CollectionID)
	}
	var list listEnvelope[Item]
	if err := c.doInternal(ctx, http.MethodGet, "/list/object/items", query, &list); err != nil {
		return nil, err
	}
	items := make([]Item, 0, len(list.Data))
	for _, item := range list.Data {
		if item.DeletedDate == nil {
			items = append(items, item)
		}
	}
	return items, nil
}

func (c *Client) GetItem(ctx context.Context, id string) (Item, error) {
	if strings.TrimSpace(id) == "" {
		return Item{}, errors.New("item ID is required")
	}
	var item Item
	if err := c.doInternal(ctx, http.MethodGet, "/object/item/"+url.PathEscape(id), nil, &item); err != nil {
		return Item{}, err
	}
	return item, nil
}

type listEnvelope[T any] struct {
	Data []T `json:"data"`
}

type responseEnvelope struct {
	Success bool           `json:"success"`
	Message string         `json:"message"`
	Data    jsontext.Value `json:"data"`
}

func (c *Client) doInternal(ctx context.Context, method, path string, query url.Values, out any) error {
	endpoint := *c.baseURL
	endpoint.Path = c.baseURL.Path + path
	endpoint.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("build bw serve request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("contact bw serve: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("read bw serve response: %w", err)
	}

	var envelope responseEnvelope
	decodeErr := json.Unmarshal(raw, &envelope)
	if resp.StatusCode < 200 || resp.StatusCode > 299 || decodeErr != nil || !envelope.Success {
		message := strings.TrimSpace(envelope.Message)
		if strings.Contains(strings.ToLower(message), "locked") {
			return ErrVaultLocked
		}
		if len(message) > 300 {
			message = message[:300]
		}
		status := resp.StatusCode
		if status >= 200 && status <= 299 {
			status = http.StatusBadGateway
		}
		return &APIError{StatusCode: status, Message: message}
	}
	if out == nil || len(envelope.Data) == 0 {
		return nil
	}
	if unmarshalErr := json.Unmarshal(envelope.Data, out); unmarshalErr != nil {
		return fmt.Errorf("decode bw serve response: %w", unmarshalErr)
	}
	return nil
}
