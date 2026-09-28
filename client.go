package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ErrNoAvatar means the user exists but has never uploaded an avatar. The
// app renders an initial-and-colour placeholder in that case.
var ErrNoAvatar = errors.New("no uploaded avatar for this user")

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = strings.NewReader(string(b))
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return nil, 0, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &errResp) == nil && errResp.Error != "" {
			return data, resp.StatusCode, fmt.Errorf("%s", errResp.Error)
		}
		return data, resp.StatusCode, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(data))
	}

	return data, resp.StatusCode, nil
}

type Project struct {
	ID              string         `json:"id"`
	WorkspaceID     string         `json:"workspaceId"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	IsFavorite      bool           `json:"isFavorite"`
	PublicToken     string         `json:"publicToken"`
	BackgroundImage string         `json:"backgroundImage"`
	BackgroundColor string         `json:"backgroundColor"`
	ColorScheme     string         `json:"colorScheme"`
	TextColor       string         `json:"textColor"`
	MyRole          string         `json:"myRole"`
	Visibility      string         `json:"visibility"`
	MyBoardRole     string         `json:"myBoardRole"`
	CanUploadAttach bool           `json:"canUploadAttachments"`
	TotalCards      int            `json:"totalCards"`
	ColumnCounts    map[string]int `json:"columnCounts"`
	WorkspaceName   string         `json:"workspaceName"`
	Columns         []Column       `json:"columns"`
	CreatedAt       string         `json:"createdAt"`
	UpdatedAt       string         `json:"updatedAt"`
}

type Column struct {
	ID        string  `json:"id"`
	Key       string  `json:"key"`
	Title     string  `json:"title"`
	Color     string  `json:"color"`
	Position  float64 `json:"position"`
	IsDone    bool    `json:"isDone"`
	CardLimit *int    `json:"cardLimit"`
}

type Card struct {
	ID        string `json:"id"`
	ProjectID string `json:"projectId"`
	Column    string `json:"column"`
	Title     string `json:"title"`
	// Description is omitted from GET /projects/{id}/cards. Use GetCard or
	// GetCardDetail for the body. omitempty keeps a board-list card from
	// looking like it has an empty description.
	Description      string  `json:"description,omitempty"`
	Position         float64 `json:"position"`
	DueDate          *string `json:"dueDate"`
	AssignedUserID   *string `json:"assignedUserId"`
	AssignedUserName *string `json:"assignedUserName"`
	Size             *int    `json:"size"`
	Priority         *string `json:"priority"`
	CreatedBy        *string `json:"createdBy"`
	CreatedByName    *string `json:"createdByName"`
	UpdatedBy        *string `json:"updatedBy"`
	UpdatedByName    *string `json:"updatedByName"`
	// Following is per-caller: whether YOU follow this card, not whether
	// anyone does. Set by assignment as well as by set_card_follow.
	Following       bool     `json:"following"`
	AttachmentCount int      `json:"attachmentCount"`
	ChecklistDone   int      `json:"checklistDone"`
	ChecklistTotal  int      `json:"checklistTotal"`
	Tags            []string `json:"tags"`
	CreatedAt       string   `json:"createdAt"`
	UpdatedAt       string   `json:"updatedAt"`
}

type Comment struct {
	ID         string `json:"id"`
	CardID     string `json:"cardId"`
	AuthorID   string `json:"authorId"`
	AuthorName string `json:"authorName"`
	AuthorImg  string `json:"authorImg,omitempty"`
	Body       string `json:"body"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

// CardDetail is the GET /cards/{id}/detail envelope: the card (with
// description) plus the collections the modal used to fetch separately.
type CardDetail struct {
	Card        Card            `json:"card"`
	Comments    []Comment       `json:"comments"`
	Events      []CardEvent     `json:"events"`
	Checklist   []ChecklistItem `json:"checklist"`
	Attachments []Attachment    `json:"attachments"`
}

// SearchResult is one GET /search hit. The openapi spec's CardSummary schema
// also documents dueDate, assignedUserId, assignedUserName and priority
// fields, but the actual search handler (internal/search/handlers.go
// resultResponse) never populates them - only the fields below are ever on
// the wire. Trust the handler: those fields are omitted here rather than
// kept as permanently-nil dead weight.
type SearchResult struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	ColumnKey   string `json:"columnKey"`
	ColumnTitle string `json:"columnTitle"`
	Title       string `json:"title"`
	// Description is a ≤180-character snippet, not the full body.
	Description string `json:"description"`
	UpdatedAt   string `json:"updatedAt"`
	Archived    bool   `json:"archived"`
}

// FocusDue is one row of GET /cards/upcoming (server schema UpcomingCard) or
// the "due" array of GET /cards/focus (server schema FocusDueCard) - the two
// are the same shape on the wire, so one Go type serves both. Note there is
// no description, updatedAt or archived field: an earlier version of the
// openapi spec merged this shape with the unrelated search-result shape
// (CardSummary) and that merge has since been split apart server-side - see
// SearchResult, which is a genuinely different response.
type FocusDue struct {
	ID          string `json:"id"`
	ProjectID   string `json:"projectId"`
	ProjectName string `json:"projectName"`
	ColumnKey   string `json:"columnKey"`
	ColumnTitle string `json:"columnTitle"`
	Title       string `json:"title"`
	DueDate     string `json:"dueDate"`
	// AssignedUserID/AssignedUserName/Priority are omitted (not null) on
	// GET /cards/upcoming, and present-but-null on the /cards/focus "due"
	// array - a *string zero value (nil) reads identically either way.
	AssignedUserID   *string `json:"assignedUserId"`
	AssignedUserName *string `json:"assignedUserName"`
	Priority         *string `json:"priority"`
}

type AttentionCard struct {
	ID               string  `json:"id"`
	ProjectID        string  `json:"projectId"`
	ProjectName      string  `json:"projectName"`
	ColumnKey        string  `json:"columnKey"`
	ColumnTitle      string  `json:"columnTitle"`
	Title            string  `json:"title"`
	UpdatedAt        string  `json:"updatedAt"`
	AssignedUserID   *string `json:"assignedUserId"`
	AssignedUserName *string `json:"assignedUserName"`
	Priority         *string `json:"priority"`
	StaleInProgress  bool    `json:"staleInProgress"`
	HighPriority     bool    `json:"highPriority"`
	IdleByMe         bool    `json:"idleByMe"`
}

type FocusResponse struct {
	Due       []FocusDue      `json:"due"`
	Attention []AttentionCard `json:"attention"`
	Hygiene   HygieneCounters `json:"hygiene"`
}

// HygieneCounters are the /cards/focus hygiene tile counts. LongInProgress
// is keyed "longInProgress" on the wire - it corresponds to the "stuck"
// value of GetHygiene's category argument (the query-string enum and the
// JSON field name differ; both are the server's naming, not a mismatch to
// fix here).
type HygieneCounters struct {
	Ghosts         int `json:"ghosts"`
	LongInProgress int `json:"longInProgress"`
	MineNoDate     int `json:"mineNoDate"`
}

// ChecklistItem is one row of a card's checklist.
type ChecklistItem struct {
	ID        string  `json:"id"`
	CardID    string  `json:"cardId"`
	Text      string  `json:"text"`
	Done      bool    `json:"done"`
	Position  float64 `json:"position"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

// CardEvent is one entry in a card's activity log. FromName/ToName are set
// only on assignee events, where From/ToValue hold user ids that the server
// has already resolved to names.
type CardEvent struct {
	ID        string  `json:"id"`
	Type      string  `json:"type"`
	FromValue *string `json:"fromValue"`
	ToValue   *string `json:"toValue"`
	FromName  *string `json:"fromName"`
	ToName    *string `json:"toName"`
	ActorID   *string `json:"actorId"`
	ActorName *string `json:"actorName"`
	CreatedAt string  `json:"createdAt"`
}

// Attachment is a file on a card. The bytes live in storage; this is only
// the metadata row.
type Attachment struct {
	ID             string `json:"id"`
	CardID         string `json:"cardId"`
	Name           string `json:"name"`
	Size           int64  `json:"size"`
	ContentType    string `json:"contentType"`
	UploadedBy     string `json:"uploadedBy"`
	UploadedByName string `json:"uploadedByName"`
	CreatedAt      string `json:"createdAt"`
}

// Member is a workspace membership row.
type Member struct {
	UserID      string        `json:"userId"`
	Email       string        `json:"email"`
	Name        string        `json:"name"`
	Role        string        `json:"role"`
	AvatarURL   string        `json:"avatarUrl"`
	JoinedAt    string        `json:"joinedAt"`
	BoardAccess []BoardAccess `json:"boardAccess,omitempty"`
}

type BoardAccess struct {
	ProjectID string `json:"projectId"`
	Name      string `json:"name"`
	Role      string `json:"role"`
}

// BoardMember is a per-board grant. Source distinguishes an explicit grant
// from one implied by workspace role.
type BoardMember struct {
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	Username  string `json:"username"`
	AvatarURL string `json:"avatarUrl"`
	Role      string `json:"role"`
	Source    string `json:"source"`
	CreatedAt string `json:"createdAt"`
}

type Invitation struct {
	ID            string        `json:"id"`
	Email         string        `json:"email"`
	Role          string        `json:"role"`
	InvitedBy     string        `json:"invitedBy"`
	InvitedByName string        `json:"invitedByName"`
	CreatedAt     string        `json:"createdAt"`
	ExpiresAt     string        `json:"expiresAt"`
	BoardGrants   []BoardAccess `json:"boardGrants,omitempty"`
	// Consumed is true when the invited email already belonged to a
	// verified account, which was added to the workspace immediately
	// instead of waiting for an accept click. Only ever set by
	// InviteMember (from the POST /invitations wrapper) - GET
	// /invitations returns bare invitations with this left false.
	Consumed bool `json:"consumed,omitempty"`
}

// Milestone is a dated marker on a board. AchievedAt is stamped once when
// achieved flips true and cleared when it flips back.
type Milestone struct {
	ID         string  `json:"id"`
	ProjectID  string  `json:"projectId"`
	Name       string  `json:"name"`
	DueOn      string  `json:"dueOn"`
	AchievedAt *string `json:"achievedAt"`
	CreatedAt  string  `json:"createdAt"`
	UpdatedAt  string  `json:"updatedAt"`
}

func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/projects", nil)
	if err != nil {
		return nil, err
	}
	var projects []Project
	if err := json.Unmarshal(data, &projects); err != nil {
		return nil, fmt.Errorf("decode projects: %w", err)
	}
	return projects, nil
}

func (c *Client) GetBoard(ctx context.Context, projectID string) (*Project, []Card, error) {
	// Fetch project (includes columns)
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(projectID), nil)
	if err != nil {
		return nil, nil, err
	}
	var project Project
	if err := json.Unmarshal(data, &project); err != nil {
		return nil, nil, fmt.Errorf("decode project: %w", err)
	}

	// Fetch cards
	data, _, err = c.do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(projectID)+"/cards", nil)
	if err != nil {
		return &project, nil, err
	}
	var cards []Card
	if err := json.Unmarshal(data, &cards); err != nil {
		return &project, nil, fmt.Errorf("decode cards: %w", err)
	}

	return &project, cards, nil
}

func (c *Client) SearchCards(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	// The query MUST be escaped. Concatenating it raw meant "#design"
	// truncated at the fragment marker and searched for the empty string
	// (zero results, no error - the worst kind of failure, and the app
	// renders tags as "#design" so it was a query people actually type),
	// "a&b" injected a stray parameter, and "50% off" produced an invalid
	// percent-escape that Cloud Run's proxy rejected with a 400 HTML page
	// before the request ever reached the app.
	q := url.Values{}
	q.Set("q", query)
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	path := "/api/v1/search?" + q.Encode()
	data, _, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Cards []SearchResult `json:"cards"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode search results: %w", err)
	}
	return resp.Cards, nil
}

func (c *Client) CreateCard(ctx context.Context, projectID string, card Card) (*Card, error) {
	body := map[string]any{
		"title":    card.Title,
		"column":   card.Column,
		"position": card.Position,
	}
	if card.Description != "" {
		body["description"] = card.Description
	}
	if card.DueDate != nil {
		body["dueDate"] = *card.DueDate
	}
	if card.AssignedUserID != nil {
		body["assignedUserId"] = *card.AssignedUserID
	}
	if card.Size != nil {
		body["size"] = *card.Size
	}
	if card.Priority != nil {
		body["priority"] = *card.Priority
	}
	if len(card.Tags) > 0 {
		body["tags"] = card.Tags
	}

	data, _, err := c.do(ctx, http.MethodPost, "/api/v1/projects/"+url.PathEscape(projectID)+"/cards", body)
	if err != nil {
		return nil, err
	}
	var created Card
	if err := json.Unmarshal(data, &created); err != nil {
		return nil, fmt.Errorf("decode card: %w", err)
	}
	return &created, nil
}

// GetCard reads a single card. Added to the platform on 2026-08-28; before
// it existed, reading one card meant pulling its entire board with GetBoard
// and filtering client-side. Board list cards omit description — this is
// the cheap way to fetch the body without the nested collections.
func (c *Client) GetCard(ctx context.Context, cardID string) (*Card, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/cards/"+url.PathEscape(cardID), nil)
	if err != nil {
		return nil, err
	}
	var card Card
	if err := json.Unmarshal(data, &card); err != nil {
		return nil, fmt.Errorf("decode card: %w", err)
	}
	return &card, nil
}

// GetCardDetail is GET /cards/{id}/detail — card (with description) plus
// comments, events (newest 100, oldest-first), checklist and attachments
// in one authorised round-trip. Prefer this over five sequential GETs
// when opening a card.
func (c *Client) GetCardDetail(ctx context.Context, cardID string) (*CardDetail, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/cards/"+url.PathEscape(cardID)+"/detail", nil)
	if err != nil {
		return nil, err
	}
	var out CardDetail
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode card detail: %w", err)
	}
	if out.Comments == nil {
		out.Comments = []Comment{}
	}
	if out.Events == nil {
		out.Events = []CardEvent{}
	}
	if out.Checklist == nil {
		out.Checklist = []ChecklistItem{}
	}
	if out.Attachments == nil {
		out.Attachments = []Attachment{}
	}
	return &out, nil
}

func (c *Client) UpdateCard(ctx context.Context, cardID string, patch map[string]any) (*Card, error) {
	data, _, err := c.do(ctx, http.MethodPatch, "/api/v1/cards/"+url.PathEscape(cardID), patch)
	if err != nil {
		return nil, err
	}
	var updated Card
	if err := json.Unmarshal(data, &updated); err != nil {
		return nil, fmt.Errorf("decode card: %w", err)
	}
	return &updated, nil
}

func (c *Client) DeleteCard(ctx context.Context, cardID string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/api/v1/cards/"+url.PathEscape(cardID), nil)
	return err
}

func (c *Client) MoveCard(ctx context.Context, cardID, targetProjectID, column string) (*Card, error) {
	body := map[string]any{
		"targetProjectId": targetProjectID,
		"column":          column,
	}
	data, _, err := c.do(ctx, http.MethodPost, "/api/v1/cards/"+url.PathEscape(cardID)+"/move", body)
	if err != nil {
		return nil, err
	}
	var moved Card
	if err := json.Unmarshal(data, &moved); err != nil {
		return nil, fmt.Errorf("decode card: %w", err)
	}
	return &moved, nil
}

func (c *Client) AddComment(ctx context.Context, cardID, body string) (*Comment, error) {
	payload := map[string]string{"body": body}
	data, _, err := c.do(ctx, http.MethodPost, "/api/v1/cards/"+url.PathEscape(cardID)+"/comments", payload)
	if err != nil {
		return nil, err
	}
	var comment Comment
	if err := json.Unmarshal(data, &comment); err != nil {
		return nil, fmt.Errorf("decode comment: %w", err)
	}
	return &comment, nil
}

func (c *Client) GetComments(ctx context.Context, cardID string) ([]Comment, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/cards/"+url.PathEscape(cardID)+"/comments", nil)
	if err != nil {
		return nil, err
	}
	var comments []Comment
	if err := json.Unmarshal(data, &comments); err != nil {
		return nil, fmt.Errorf("decode comments: %w", err)
	}
	return comments, nil
}

func (c *Client) GetUpcoming(ctx context.Context) ([]FocusDue, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/cards/upcoming", nil)
	if err != nil {
		return nil, err
	}
	var cards []FocusDue
	if err := json.Unmarshal(data, &cards); err != nil {
		return nil, fmt.Errorf("decode upcoming: %w", err)
	}
	return cards, nil
}

func (c *Client) GetFocus(ctx context.Context) (*FocusResponse, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/cards/focus", nil)
	if err != nil {
		return nil, err
	}
	var resp FocusResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode focus: %w", err)
	}
	return &resp, nil
}

// ── Bulk ──────────────────────────────────────────────────────────
//
// All three bulk endpoints are all-or-nothing: one bad item rolls the whole
// batch back and the error names the offending index, so a failed call never
// leaves a half-applied board. They also send no notification emails, and
// cap at 100 items. One request spends one rate-limit token, against a
// 60-burst budget - looping single writes is what exhausts it.

// BulkCreateCards creates up to 100 cards in one transaction. Unlike the
// single create, items take full metadata directly, and the assignee is NOT
// defaulted to the caller.
func (c *Client) BulkCreateCards(ctx context.Context, projectID string, cards []map[string]any) ([]Card, error) {
	return c.bulkCards(ctx, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(projectID)+"/cards/bulk",
		map[string]any{"cards": cards})
}

// BulkUpdateCards applies up to 100 partial updates. Each item is {id, ...}
// with the same semantics as a single PATCH: present = apply, null = clear,
// absent = leave alone.
func (c *Client) BulkUpdateCards(ctx context.Context, patches []map[string]any) ([]Card, error) {
	return c.bulkCards(ctx, http.MethodPatch, "/api/v1/cards/bulk",
		map[string]any{"cards": patches})
}

// BulkMoveCards sweeps up to 100 cards into a column. Without
// targetProjectID every id must be on the SAME board; with it, the batch
// moves to that board (same workspace only) and appends to the column.
func (c *Client) BulkMoveCards(ctx context.Context, ids []string, column, targetProjectID string) ([]Card, error) {
	body := map[string]any{"ids": ids, "column": column}
	if targetProjectID != "" {
		body["targetProjectId"] = targetProjectID
	}
	return c.bulkCards(ctx, http.MethodPost, "/api/v1/cards/bulk/move", body)
}

func (c *Client) bulkCards(ctx context.Context, method, path string, body any) ([]Card, error) {
	data, _, err := c.do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Cards []Card `json:"cards"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode bulk response: %w", err)
	}
	return resp.Cards, nil
}

// ── Checklist ─────────────────────────────────────────────────────

func (c *Client) GetChecklist(ctx context.Context, cardID string) ([]ChecklistItem, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/cards/"+url.PathEscape(cardID)+"/checklist", nil)
	if err != nil {
		return nil, err
	}
	var items []ChecklistItem
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, fmt.Errorf("decode checklist: %w", err)
	}
	return items, nil
}

// AddChecklistItem appends to the end of the card's checklist.
func (c *Client) AddChecklistItem(ctx context.Context, cardID, text string) (*ChecklistItem, error) {
	data, _, err := c.do(ctx, http.MethodPost,
		"/api/v1/cards/"+url.PathEscape(cardID)+"/checklist",
		map[string]any{"text": text})
	if err != nil {
		return nil, err
	}
	var it ChecklistItem
	if err := json.Unmarshal(data, &it); err != nil {
		return nil, fmt.Errorf("decode checklist item: %w", err)
	}
	return &it, nil
}

// UpdateChecklistItem is a partial update - pass only what changes.
func (c *Client) UpdateChecklistItem(ctx context.Context, itemID string, patch map[string]any) (*ChecklistItem, error) {
	data, _, err := c.do(ctx, http.MethodPatch, "/api/v1/checklist/"+url.PathEscape(itemID), patch)
	if err != nil {
		return nil, err
	}
	var it ChecklistItem
	if err := json.Unmarshal(data, &it); err != nil {
		return nil, fmt.Errorf("decode checklist item: %w", err)
	}
	return &it, nil
}

func (c *Client) DeleteChecklistItem(ctx context.Context, itemID string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/api/v1/checklist/"+url.PathEscape(itemID), nil)
	return err
}

// ── Activity + hygiene ────────────────────────────────────────────

// GetCardEvents returns a card's activity log, oldest first.
func (c *Client) GetCardEvents(ctx context.Context, cardID string) ([]CardEvent, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/cards/"+url.PathEscape(cardID)+"/events", nil)
	if err != nil {
		return nil, err
	}
	var events []CardEvent
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("decode card events: %w", err)
	}
	return events, nil
}

// GetHygiene drills into one of the counters GetFocus reports. Category is
// one of ghosts, stuck, mine_no_date.
func (c *Client) GetHygiene(ctx context.Context, category string) ([]AttentionCard, error) {
	q := url.Values{}
	q.Set("type", category)
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/cards/hygiene?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	var cards []AttentionCard
	if err := json.Unmarshal(data, &cards); err != nil {
		return nil, fmt.Errorf("decode hygiene cards: %w", err)
	}
	return cards, nil
}

// ── Attachments ───────────────────────────────────────────────────

func (c *Client) ListAttachments(ctx context.Context, cardID string) ([]Attachment, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/cards/"+url.PathEscape(cardID)+"/attachments", nil)
	if err != nil {
		return nil, err
	}
	var out []Attachment
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode attachments: %w", err)
	}
	return out, nil
}

// DownloadAttachment streams the stored bytes back along with the
// Content-Type the server serves them as.
func (c *Client) DownloadAttachment(ctx context.Context, attachmentID string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/v1/attachments/"+url.PathEscape(attachmentID), nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		// The download route answers with a bare 404 (no JSON body) when the
		// attachment or its bytes are missing, so don't try to parse one.
		return nil, "", fmt.Errorf("HTTP %d downloading attachment", resp.StatusCode)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// UploadAttachment posts a single multipart "file" part. This server runs
// remotely (Cloud Run), so it has no access to the caller's filesystem -
// bytes have to arrive inline rather than as a path.
func (c *Client) UploadAttachment(ctx context.Context, cardID, filename, contentType string, data []byte) (*Attachment, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{
		fmt.Sprintf(`form-data; name="file"; filename=%q`, filename),
	}
	if contentType != "" {
		h["Content-Type"] = []string{contentType}
	}
	part, err := w.CreatePart(h)
	if err != nil {
		return nil, fmt.Errorf("build multipart: %w", err)
	}
	if _, err := part.Write(data); err != nil {
		return nil, fmt.Errorf("write multipart: %w", err)
	}
	if err := w.Close(); err != nil {
		return nil, fmt.Errorf("close multipart: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.baseURL+"/api/v1/cards/"+url.PathEscape(cardID)+"/attachments", &buf)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		var errResp struct {
			Error       string `json:"error"`
			CurrentPlan string `json:"currentPlan"`
		}
		if json.Unmarshal(body, &errResp) == nil && errResp.Error != "" {
			// 402 carries the plan, which is the actionable part.
			if resp.StatusCode == http.StatusPaymentRequired && errResp.CurrentPlan != "" {
				return nil, fmt.Errorf("%s (workspace plan: %s)", errResp.Error, errResp.CurrentPlan)
			}
			return nil, fmt.Errorf("%s", errResp.Error)
		}
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var att Attachment
	if err := json.Unmarshal(body, &att); err != nil {
		return nil, fmt.Errorf("decode attachment: %w", err)
	}
	return &att, nil
}

// ── Projects ──────────────────────────────────────────────────────

func (c *Client) GetProject(ctx context.Context, projectID string) (*Project, error) {
	return c.projectCall(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(projectID), nil)
}

// CreateProject creates a board. template is a key from ListBoardTemplates
// (e.g. "sprint", "bug-tracker"); empty uses the default "kanban" layout (To
// Do / In Progress / Done). An unknown key is rejected with a 400.
//
// The returned Project's Columns is always empty (POST /projects never
// populates it, unlike GET /projects/{id}) regardless of template - call
// GetProject on the new id to read the real columns. Only the default
// template seeds the historical todo/doing/done column keys anyway - every
// other template generates its own, so that follow-up call is required
// before creating cards on a templated board.
func (c *Client) CreateProject(ctx context.Context, name, description, template string) (*Project, error) {
	body := map[string]any{"name": name}
	if description != "" {
		body["description"] = description
	}
	if template != "" {
		body["template"] = template
	}
	return c.projectCall(ctx, http.MethodPost, "/api/v1/projects", body)
}

func (c *Client) UpdateProject(ctx context.Context, projectID string, patch map[string]any) (*Project, error) {
	return c.projectCall(ctx, http.MethodPatch, "/api/v1/projects/"+url.PathEscape(projectID), patch)
}

// DeleteProject cascades: columns, cards, comments and attachments go too.
func (c *Client) DeleteProject(ctx context.Context, projectID string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/api/v1/projects/"+url.PathEscape(projectID), nil)
	return err
}

func (c *Client) projectCall(ctx context.Context, method, path string, body any) (*Project, error) {
	data, _, err := c.do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	var p Project
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("decode project: %w", err)
	}
	return &p, nil
}

// ExportProject returns the raw export payload. format is "json" or "csv";
// the caller gets bytes because CSV is not JSON.
func (c *Client) ExportProject(ctx context.Context, projectID, format string) ([]byte, error) {
	path := "/api/v1/projects/" + url.PathEscape(projectID) + "/export"
	if format != "" {
		q := url.Values{}
		q.Set("format", format)
		path += "?" + q.Encode()
	}
	data, _, err := c.do(ctx, http.MethodGet, path, nil)
	return data, err
}

// ── Columns ───────────────────────────────────────────────────────

func (c *Client) AddColumn(ctx context.Context, projectID, title string) (*Column, error) {
	return c.columnCall(ctx, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(projectID)+"/columns",
		map[string]any{"title": title})
}

func (c *Client) UpdateColumn(ctx context.Context, columnID string, patch map[string]any) (*Column, error) {
	return c.columnCall(ctx, http.MethodPatch, "/api/v1/columns/"+url.PathEscape(columnID), patch)
}

// DeleteColumn refuses (409) when the column still holds cards - archive or
// move them first.
func (c *Client) DeleteColumn(ctx context.Context, columnID string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/api/v1/columns/"+url.PathEscape(columnID), nil)
	return err
}

// ArchiveColumn archives every LIVE card in the column and returns how many
// it moved plus their ids (so a caller can undo via RestoreCard). The server
// only archives cards in a column flagged is_done, and answers 200 either
// way - so a column that is not a "done" column comes back with 0 and no
// ids rather than an error. Returning the count and ids is what makes that
// no-op visible to the caller.
func (c *Client) ArchiveColumn(ctx context.Context, columnID string) (int, []string, error) {
	data, _, err := c.do(ctx, http.MethodPost, "/api/v1/columns/"+url.PathEscape(columnID)+"/archive", nil)
	if err != nil {
		return 0, nil, err
	}
	var resp struct {
		Archived int      `json:"archived"`
		IDs      []string `json:"ids"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return 0, nil, fmt.Errorf("decode archive response: %w", err)
	}
	return resp.Archived, resp.IDs, nil
}

func (c *Client) columnCall(ctx context.Context, method, path string, body any) (*Column, error) {
	data, _, err := c.do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	var col Column
	if err := json.Unmarshal(data, &col); err != nil {
		return nil, fmt.Errorf("decode column: %w", err)
	}
	return &col, nil
}

// ── Members + invitations ─────────────────────────────────────────

func (c *Client) ListMembers(ctx context.Context) ([]Member, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/members", nil)
	if err != nil {
		return nil, err
	}
	var out []Member
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode members: %w", err)
	}
	return out, nil
}

func (c *Client) UpdateMemberRole(ctx context.Context, userID, role string) error {
	_, _, err := c.do(ctx, http.MethodPatch, "/api/v1/members/"+url.PathEscape(userID),
		map[string]any{"role": role})
	return err
}

func (c *Client) RemoveMember(ctx context.Context, userID string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/api/v1/members/"+url.PathEscape(userID), nil)
	return err
}

func (c *Client) ListInvitations(ctx context.Context) ([]Invitation, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/invitations", nil)
	if err != nil {
		return nil, err
	}
	var out []Invitation
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode invitations: %w", err)
	}
	return out, nil
}

// InviteMember sends an invitation. boardGrants is honoured only for
// role "member" - admins implicitly see every board.
//
// POST /invitations answers a wrapper, {"invitation": {...}, "consumed":
// bool} - NOT a bare Invitation - so unwrapping here is load-bearing;
// decoding the wrapper directly as an Invitation used to silently produce
// an empty id/email/etc. consumed=true means the invited address already
// belonged to a verified account, which was added to the workspace on the
// spot rather than waiting on an accept click. A bare Invitation object is
// tolerated too, in case the server ever stops wrapping this response.
func (c *Client) InviteMember(ctx context.Context, email, role string, boardGrants []map[string]any) (*Invitation, error) {
	body := map[string]any{"email": email, "role": role}
	if len(boardGrants) > 0 {
		body["boardGrants"] = boardGrants
	}
	data, _, err := c.do(ctx, http.MethodPost, "/api/v1/invitations", body)
	if err != nil {
		return nil, err
	}
	var wrapper struct {
		Invitation *Invitation `json:"invitation"`
		Consumed   bool        `json:"consumed"`
	}
	if err := json.Unmarshal(data, &wrapper); err == nil && wrapper.Invitation != nil {
		inv := *wrapper.Invitation
		inv.Consumed = wrapper.Consumed
		return &inv, nil
	}
	var inv Invitation
	if err := json.Unmarshal(data, &inv); err != nil {
		return nil, fmt.Errorf("decode invitation: %w", err)
	}
	return &inv, nil
}

func (c *Client) RevokeInvitation(ctx context.Context, invitationID string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/api/v1/invitations/"+url.PathEscape(invitationID), nil)
	return err
}

func (c *Client) ListBoardMembers(ctx context.Context, projectID string) ([]BoardMember, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(projectID)+"/members", nil)
	if err != nil {
		return nil, err
	}
	var out []BoardMember
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode board members: %w", err)
	}
	return out, nil
}

func (c *Client) GrantBoardAccess(ctx context.Context, projectID, userID, role string) error {
	_, _, err := c.do(ctx, http.MethodPost, "/api/v1/projects/"+url.PathEscape(projectID)+"/members",
		map[string]any{"userId": userID, "role": role})
	return err
}

func (c *Client) RevokeBoardAccess(ctx context.Context, projectID, userID string) error {
	_, _, err := c.do(ctx, http.MethodDelete,
		"/api/v1/projects/"+url.PathEscape(projectID)+"/members/"+url.PathEscape(userID), nil)
	return err
}

// ── Comment edit / delete ─────────────────────────────────────────

// UpdateComment is author-only.
func (c *Client) UpdateComment(ctx context.Context, commentID, body string) (*Comment, error) {
	data, _, err := c.do(ctx, http.MethodPatch, "/api/v1/comments/"+url.PathEscape(commentID),
		map[string]any{"body": body})
	if err != nil {
		return nil, err
	}
	var cm Comment
	if err := json.Unmarshal(data, &cm); err != nil {
		return nil, fmt.Errorf("decode comment: %w", err)
	}
	return &cm, nil
}

// DeleteComment is allowed for the author, or a workspace owner/admin.
func (c *Client) DeleteComment(ctx context.Context, commentID string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/api/v1/comments/"+url.PathEscape(commentID), nil)
	return err
}

// ── Attachment delete + card follow ───────────────────────────────

func (c *Client) DeleteAttachment(ctx context.Context, attachmentID string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/api/v1/attachments/"+url.PathEscape(attachmentID), nil)
	return err
}

func (c *Client) SetCardFollow(ctx context.Context, cardID string, follow bool) error {
	method := http.MethodDelete
	if follow {
		method = http.MethodPost
	}
	_, _, err := c.do(ctx, method, "/api/v1/cards/"+url.PathEscape(cardID)+"/follow", nil)
	return err
}

// ListTags returns the board's distinct tags (live AND archived cards),
// sorted and de-duplicated - what the tag autocomplete is built on.
//
// Note the auth model differs from most of this API: a caller without board
// access gets an EMPTY list, not a 404. An empty result therefore means
// "no tags OR no access", and the two are indistinguishable.
func (c *Client) ListTags(ctx context.Context, projectID string) ([]string, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(projectID)+"/tags", nil)
	if err != nil {
		return nil, err
	}
	var tags []string
	if err := json.Unmarshal(data, &tags); err != nil {
		return nil, fmt.Errorf("decode tags: %w", err)
	}
	return tags, nil
}

// ── Milestones ────────────────────────────────────────────────────

func (c *Client) ListMilestones(ctx context.Context, projectID string) ([]Milestone, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(projectID)+"/milestones", nil)
	if err != nil {
		return nil, err
	}
	var out []Milestone
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode milestones: %w", err)
	}
	return out, nil
}

// AddMilestone creates one. Board admins only. dueOn is a YYYY-MM-DD date,
// not a timestamp.
func (c *Client) AddMilestone(ctx context.Context, projectID, name, dueOn string) (*Milestone, error) {
	return c.milestoneCall(ctx, http.MethodPost,
		"/api/v1/projects/"+url.PathEscape(projectID)+"/milestones",
		map[string]any{"name": name, "dueOn": dueOn})
}

func (c *Client) UpdateMilestone(ctx context.Context, milestoneID string, patch map[string]any) (*Milestone, error) {
	return c.milestoneCall(ctx, http.MethodPatch, "/api/v1/milestones/"+url.PathEscape(milestoneID), patch)
}

func (c *Client) DeleteMilestone(ctx context.Context, milestoneID string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/api/v1/milestones/"+url.PathEscape(milestoneID), nil)
	return err
}

func (c *Client) milestoneCall(ctx context.Context, method, path string, body any) (*Milestone, error) {
	data, _, err := c.do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	var m Milestone
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("decode milestone: %w", err)
	}
	return &m, nil
}

// ── Public sharing ────────────────────────────────────────────────

// EnableShare turns on read-only public sharing and returns the token.
// Idempotent: an already-shared board returns its existing token, so the
// URL stays stable rather than rotating on every call.
func (c *Client) EnableShare(ctx context.Context, projectID string) (string, error) {
	data, _, err := c.do(ctx, http.MethodPost, "/api/v1/projects/"+url.PathEscape(projectID)+"/share", nil)
	if err != nil {
		return "", err
	}
	var resp struct {
		PublicToken string `json:"publicToken"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("decode share token: %w", err)
	}
	return resp.PublicToken, nil
}

// DisableShare revokes the link. The token column is UNIQUE, so re-enabling
// later mints a fresh one - old links stay dead.
func (c *Client) DisableShare(ctx context.Context, projectID string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/api/v1/projects/"+url.PathEscape(projectID)+"/share", nil)
	return err
}

// ── Card archive / restore ───────────────────────────────────────

// ArchivedCard is one row of GET /projects/{id}/cards/archived: the same
// lean board projection GetBoard returns (description omitted) plus
// archivedAt.
type ArchivedCard struct {
	Card
	ArchivedAt string `json:"archivedAt"`
}

// ArchiveCard takes a card off its board without deleting it - it stays
// searchable and shows up in ListArchivedCards until restored. Archiving an
// already-archived card, or one on an inaccessible board, is a 404.
func (c *Client) ArchiveCard(ctx context.Context, cardID string) error {
	_, _, err := c.do(ctx, http.MethodPost, "/api/v1/cards/"+url.PathEscape(cardID)+"/archive", nil)
	return err
}

// RestoreCard un-archives a card, returning it to the column and position it
// was archived from. Restoring a card that isn't archived is a 404.
func (c *Client) RestoreCard(ctx context.Context, cardID string) (*Card, error) {
	data, _, err := c.do(ctx, http.MethodPost, "/api/v1/cards/"+url.PathEscape(cardID)+"/restore", nil)
	if err != nil {
		return nil, err
	}
	var card Card
	if err := json.Unmarshal(data, &card); err != nil {
		return nil, fmt.Errorf("decode card: %w", err)
	}
	return &card, nil
}

// ListArchivedCards returns a board's archived cards, most recently archived
// first, capped at 200. Older archived cards beyond the cap stay reachable
// through SearchCards. An inaccessible board returns an empty list rather
// than an error, same posture as GetBoard.
func (c *Client) ListArchivedCards(ctx context.Context, projectID string) ([]ArchivedCard, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(projectID)+"/cards/archived", nil)
	if err != nil {
		return nil, err
	}
	var out []ArchivedCard
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode archived cards: %w", err)
	}
	return out, nil
}

// ── Board templates ──────────────────────────────────────────────

// BoardTemplateColumn is one column a board template seeds.
type BoardTemplateColumn struct {
	Title  string `json:"title"`
	Color  string `json:"color"` // palette token, or empty for the default
	IsDone bool   `json:"isDone"`
}

// BoardTemplate is one entry in the fixed catalogue GET /board-templates
// returns. Pass Key as CreateProject's template argument.
type BoardTemplate struct {
	Key         string                `json:"key"`
	Name        string                `json:"name"`
	Description string                `json:"description"`
	Columns     []BoardTemplateColumn `json:"columns"`
}

// ListBoardTemplates returns the fixed template catalogue - the same for
// every caller, any authenticated user, no plan gate.
func (c *Client) ListBoardTemplates(ctx context.Context) ([]BoardTemplate, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/board-templates", nil)
	if err != nil {
		return nil, err
	}
	var out []BoardTemplate
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("decode board templates: %w", err)
	}
	return out, nil
}

// ── Webhooks (outbound) ──────────────────────────────────────────

// WebhookDelivery is one recent delivery attempt for a webhook subscription.
//
// Note: Status is "delivered" or "failed" on the wire - the openapi spec's
// WebhookDelivery.status documents an enum of [success, failed], but the
// handler (internal/webhooks/store.go StatusDelivered/StatusFailed) never
// emits "success". Trust the handler: this is left as a plain string rather
// than a Go enum so decoding never breaks if that gets reconciled either way.
type WebhookDelivery struct {
	Event      string `json:"event"`
	Status     string `json:"status"`
	StatusCode int    `json:"statusCode"`
	Attempts   int    `json:"attempts"`
	Error      string `json:"error"`
	CreatedAt  string `json:"createdAt"`
}

// WebhookSubscription is a board's outbound webhook. Secret is returned on
// EVERY read (list, create, update) - not just at creation - so handle it as
// sensitive: it signs "craaft"-format deliveries via the X-Craaft-Signature
// header. Slack/Discord deliveries are unsigned; those services authenticate
// by the secret URL itself.
type WebhookSubscription struct {
	// ID is the subscription id - the handle for UpdateWebhook/DeleteWebhook.
	// It is NOT the same as EndpointID.
	ID               string            `json:"id"`
	EndpointID       string            `json:"endpointId"`
	URL              string            `json:"url"`
	Secret           string            `json:"secret"`
	Description      string            `json:"description"`
	Format           string            `json:"format"` // "craaft" | "slack" | "discord"
	Events           []string          `json:"events"` // empty = subscribed to every event
	Active           bool              `json:"active"`
	CreatedAt        string            `json:"createdAt"`
	RecentDeliveries []WebhookDelivery `json:"recentDeliveries"`
}

// ListWebhooks returns a board's webhook subscriptions plus the catalogue of
// broadcast event names valid in a subscription's event filter. Board admins
// only; a Free workspace gets 402.
func (c *Client) ListWebhooks(ctx context.Context, projectID string) ([]WebhookSubscription, []string, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(projectID)+"/webhooks", nil)
	if err != nil {
		return nil, nil, err
	}
	var resp struct {
		Webhooks       []WebhookSubscription `json:"webhooks"`
		EventCatalogue []string              `json:"eventCatalogue"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, nil, fmt.Errorf("decode webhooks: %w", err)
	}
	return resp.Webhooks, resp.EventCatalogue, nil
}

// CreateWebhook registers a new outbound webhook on a board. format is
// "craaft" (default, signed JSON envelope), "slack" or "discord" (a message
// posted to those services' incoming-webhook URLs, unsigned); empty means
// "craaft". events filters which broadcast events are delivered; empty means
// all. Board admins on Pro boards only - a Free workspace gets 402. The URL
// must be absolute http(s); a private, loopback or link-local address is
// rejected, and delivery re-checks the resolved address so DNS can't route
// around it.
func (c *Client) CreateWebhook(ctx context.Context, projectID, targetURL, description, format string, events []string) (*WebhookSubscription, error) {
	body := map[string]any{"url": targetURL}
	if description != "" {
		body["description"] = description
	}
	if format != "" {
		body["format"] = format
	}
	if len(events) > 0 {
		body["events"] = events
	}
	data, _, err := c.do(ctx, http.MethodPost, "/api/v1/projects/"+url.PathEscape(projectID)+"/webhooks", body)
	if err != nil {
		return nil, err
	}
	var wh WebhookSubscription
	if err := json.Unmarshal(data, &wh); err != nil {
		return nil, fmt.Errorf("decode webhook: %w", err)
	}
	return &wh, nil
}

// UpdateWebhook partially updates a webhook subscription - pass only what
// changes. subscriptionID is WebhookSubscription.ID, not EndpointID. Needs
// Pro: a downgraded workspace gets 402 here even though DeleteWebhook stays
// open so it can still clean up.
func (c *Client) UpdateWebhook(ctx context.Context, subscriptionID string, patch map[string]any) (*WebhookSubscription, error) {
	data, _, err := c.do(ctx, http.MethodPatch, "/api/v1/webhooks/"+url.PathEscape(subscriptionID), patch)
	if err != nil {
		return nil, err
	}
	var wh WebhookSubscription
	if err := json.Unmarshal(data, &wh); err != nil {
		return nil, fmt.Errorf("decode webhook: %w", err)
	}
	return &wh, nil
}

// DeleteWebhook removes a webhook subscription. Not plan-gated: a downgraded
// workspace must still be able to clean up.
func (c *Client) DeleteWebhook(ctx context.Context, subscriptionID string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/api/v1/webhooks/"+url.PathEscape(subscriptionID), nil)
	return err
}

// ── Email-to-card (inbound email) ────────────────────────────────

// InboundAddress is a board's email-to-card intake address.
type InboundAddress struct {
	Email        string  `json:"email"`
	Token        string  `json:"token"`
	TargetColumn *string `json:"targetColumn"` // column key; nil = board's first column
	Active       bool    `json:"active"`
	CreatedAt    string  `json:"createdAt"`
}

// GetInboundEmail returns a board's inbound intake address, or (false, nil)
// when email-to-card has never been enabled for it. Board admins on Pro
// boards only.
func (c *Client) GetInboundEmail(ctx context.Context, projectID string) (bool, *InboundAddress, error) {
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+url.PathEscape(projectID)+"/inbound-email", nil)
	if err != nil {
		return false, nil, err
	}
	var resp struct {
		Enabled bool            `json:"enabled"`
		Address *InboundAddress `json:"address"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return false, nil, fmt.Errorf("decode inbound email: %w", err)
	}
	return resp.Enabled, resp.Address, nil
}

// EnableInboundEmail mints a board's intake address. targetColumn is a
// column key; nil defaults to the board's first column. Returns an error
// (409 from the API) if the board already has an address - use
// UpdateInboundEmail or DisableInboundEmail first. Board admins on Pro
// boards only.
func (c *Client) EnableInboundEmail(ctx context.Context, projectID string, targetColumn *string) (*InboundAddress, error) {
	body := map[string]any{}
	if targetColumn != nil {
		body["targetColumn"] = *targetColumn
	}
	data, _, err := c.do(ctx, http.MethodPost, "/api/v1/projects/"+url.PathEscape(projectID)+"/inbound-email", body)
	if err != nil {
		return nil, err
	}
	var addr InboundAddress
	if err := json.Unmarshal(data, &addr); err != nil {
		return nil, fmt.Errorf("decode inbound address: %w", err)
	}
	return &addr, nil
}

// UpdateInboundEmail partially updates a board's inbound email config - pass
// only what changes. A "rotate":true entry in patch mints a new token,
// immediately invalidating the old address. Needs Pro; DisableInboundEmail
// stays ungated so a downgraded workspace can still switch it off.
func (c *Client) UpdateInboundEmail(ctx context.Context, projectID string, patch map[string]any) (*InboundAddress, error) {
	data, _, err := c.do(ctx, http.MethodPatch, "/api/v1/projects/"+url.PathEscape(projectID)+"/inbound-email", patch)
	if err != nil {
		return nil, err
	}
	var addr InboundAddress
	if err := json.Unmarshal(data, &addr); err != nil {
		return nil, fmt.Errorf("decode inbound address: %w", err)
	}
	return &addr, nil
}

// DisableInboundEmail turns off a board's email-to-card intake. Not
// plan-gated, so a downgraded workspace can still disable it.
func (c *Client) DisableInboundEmail(ctx context.Context, projectID string) error {
	_, _, err := c.do(ctx, http.MethodDelete, "/api/v1/projects/"+url.PathEscape(projectID)+"/inbound-email", nil)
	return err
}

// ── Avatar ────────────────────────────────────────────────────────

// GetAvatar streams a user's avatar bytes. Returns ErrNoAvatar when the user
// has never uploaded one - the server answers a bare 404 for that, which is
// a normal outcome rather than a failure.
func (c *Client) GetAvatar(ctx context.Context, userID string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/api/v1/users/"+url.PathEscape(userID)+"/avatar", nil)
	if err != nil {
		return nil, "", fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("execute request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, "", ErrNoAvatar
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode >= 400 {
		return nil, "", fmt.Errorf("HTTP %d fetching avatar", resp.StatusCode)
	}
	return data, resp.Header.Get("Content-Type"), nil
}
