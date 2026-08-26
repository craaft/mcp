package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

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
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Description      string         `json:"description"`
	IsFavorite       bool           `json:"isFavorite"`
	PublicToken      string         `json:"publicToken"`
	BackgroundImage  string         `json:"backgroundImage"`
	BackgroundColor  string         `json:"backgroundColor"`
	ColorScheme      string         `json:"colorScheme"`
	TextColor        string         `json:"textColor"`
	MyRole           string         `json:"myRole"`
	Visibility       string         `json:"visibility"`
	MyBoardRole      string         `json:"myBoardRole"`
	CanUploadAttach  bool           `json:"canUploadAttachments"`
	TotalCards       int            `json:"totalCards"`
	ColumnCounts     map[string]int `json:"columnCounts"`
	WorkspaceName    string         `json:"workspaceName"`
	Columns          []Column       `json:"columns"`
	CreatedAt        string         `json:"createdAt"`
	UpdatedAt        string         `json:"updatedAt"`
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
	ID               string   `json:"id"`
	ProjectID        string   `json:"projectId"`
	Column           string   `json:"column"`
	Title            string   `json:"title"`
	Description      string   `json:"description"`
	Position         float64  `json:"position"`
	DueDate          *string  `json:"dueDate"`
	AssignedUserID   *string  `json:"assignedUserId"`
	AssignedUserName *string  `json:"assignedUserName"`
	Size             *int     `json:"size"`
	Priority         *string  `json:"priority"`
	CreatedBy        *string  `json:"createdBy"`
	CreatedByName    *string  `json:"createdByName"`
	AttachmentCount  int      `json:"attachmentCount"`
	ChecklistDone    int      `json:"checklistDone"`
	ChecklistTotal   int      `json:"checklistTotal"`
	Tags             []string `json:"tags"`
	CreatedAt        string   `json:"createdAt"`
	UpdatedAt        string   `json:"updatedAt"`
}

type Comment struct {
	ID         string `json:"id"`
	CardID     string `json:"cardId"`
	AuthorID   string `json:"authorId"`
	AuthorName string `json:"authorName"`
	Body       string `json:"body"`
	CreatedAt  string `json:"createdAt"`
	UpdatedAt  string `json:"updatedAt"`
}

type SearchResult struct {
	ID           string  `json:"id"`
	ProjectID    string  `json:"projectId"`
	ProjectName  string  `json:"projectName"`
	ColumnKey    string  `json:"columnKey"`
	ColumnTitle  string  `json:"columnTitle"`
	Title        string  `json:"title"`
	Description  string  `json:"description"`
	DueDate      *string `json:"dueDate"`
	AssignedUser *string `json:"assignedUserName"`
	Priority     *string `json:"priority"`
	UpdatedAt    string  `json:"updatedAt"`
	Archived     bool    `json:"archived"`
}

type FocusDue struct {
	ID               string  `json:"id"`
	ProjectID        string  `json:"projectId"`
	ProjectName      string  `json:"projectName"`
	ColumnKey        string  `json:"columnKey"`
	ColumnTitle      string  `json:"columnTitle"`
	Title            string  `json:"title"`
	DueDate          string  `json:"dueDate"`
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
	AssignedUserName *string `json:"assignedUserName"`
	Priority         *string `json:"priority"`
	StaleInProgress  bool    `json:"staleInProgress"`
	HighPriority     bool    `json:"highPriority"`
	IdleByMe         bool    `json:"idleByMe"`
}

type FocusResponse struct {
	Due        []FocusDue       `json:"due"`
	Attention  []AttentionCard  `json:"attention"`
	Hygiene    HygieneCounters  `json:"hygiene"`
}

type HygieneCounters struct {
	Ghosts     int `json:"ghosts"`
	Stuck      int `json:"stuck"`
	MineNoDate int `json:"mineNoDate"`
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
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/projects/"+projectID, nil)
	if err != nil {
		return nil, nil, err
	}
	var project Project
	if err := json.Unmarshal(data, &project); err != nil {
		return nil, nil, fmt.Errorf("decode project: %w", err)
	}

	// Fetch cards
	data, _, err = c.do(ctx, http.MethodGet, "/api/v1/projects/"+projectID+"/cards", nil)
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
	path := "/api/v1/search?q=" + query
	if limit > 0 {
		path += fmt.Sprintf("&limit=%d", limit)
	}
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

	data, _, err := c.do(ctx, http.MethodPost, "/api/v1/projects/"+projectID+"/cards", body)
	if err != nil {
		return nil, err
	}
	var created Card
	if err := json.Unmarshal(data, &created); err != nil {
		return nil, fmt.Errorf("decode card: %w", err)
	}
	return &created, nil
}

func (c *Client) UpdateCard(ctx context.Context, cardID string, patch map[string]any) (*Card, error) {
	data, _, err := c.do(ctx, http.MethodPatch, "/api/v1/cards/"+cardID, patch)
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
	_, _, err := c.do(ctx, http.MethodDelete, "/api/v1/cards/"+cardID, nil)
	return err
}

func (c *Client) MoveCard(ctx context.Context, cardID, targetProjectID, column string) (*Card, error) {
	body := map[string]any{
		"targetProjectId": targetProjectID,
		"column":          column,
	}
	data, _, err := c.do(ctx, http.MethodPost, "/api/v1/cards/"+cardID+"/move", body)
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
	data, _, err := c.do(ctx, http.MethodPost, "/api/v1/cards/"+cardID+"/comments", payload)
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
	data, _, err := c.do(ctx, http.MethodGet, "/api/v1/cards/"+cardID+"/comments", nil)
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
