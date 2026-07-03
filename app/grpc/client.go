package grpc

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"net/http"

	apiv1 "github.com/dojo-product/ms1-proto/sdk-go/cmsbff/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

//go:embed stub.json
var stubJSON []byte

type Client struct {
	conn            *grpc.ClientConn
	svc             apiv1.CmsBffV1ServiceClient
	contentfulURL   string
	contentfulToken string
	httpClient      *http.Client
}

func NewClient(addr, contentfulSpaceID, contentfulEnvironment, contentfulToken string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("grpc.NewClient: %w", err)
	}

	var contentfulURL string
	if contentfulSpaceID != "" && contentfulEnvironment != "" {
		contentfulURL = fmt.Sprintf("https://graphql.contentful.com/content/v1/spaces/%s/environments/%s", contentfulSpaceID, contentfulEnvironment)
	}

	return &Client{
		conn:            conn,
		svc:             apiv1.NewCmsBffV1ServiceClient(conn),
		contentfulURL:   contentfulURL,
		contentfulToken: contentfulToken,
		httpClient:      &http.Client{},
	}, nil
}

const getConceptQuery = `
query GetConcept($id: String!, $locale: String!) {
    concept(id: $id, locale: $locale) {
        sys { id }
        ms1Title
        body
    }
}`

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables"`
}

type gqlConceptResponse struct {
	Data struct {
		Concept struct {
			Sys struct {
				ID string `json:"id"`
			} `json:"sys"`
			Title string `json:"ms1Title"`
			Body  string `json:"body"`
		} `json:"concept"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

func (c *Client) fetchConceptBody(ctx context.Context, conceptID, locale string) (string, error) {
	payload, err := json.Marshal(gqlRequest{
		Query: getConceptQuery,
		Variables: map[string]any{
			"id":     conceptID,
			"locale": locale,
		},
	})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.contentfulURL, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.contentfulToken)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result gqlConceptResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	if len(result.Errors) > 0 {
		return "", fmt.Errorf("contentful: %s", result.Errors[0].Message)
	}
	return result.Data.Concept.Body, nil
}

// UnitContent holds the content for a single unit, ready to send to the LLM.
type UnitContent struct {
	UnitTitle string `json:"unit_title"`
	Content   string `json:"content"`
}

// ModuleContent holds all unit chunks plus the localized module titles.
type ModuleContent struct {
	TitleEN string        `json:"title_en"`
	TitleJA string        `json:"title_ja"`
	Units   []UnitContent `json:"units"`
}

func (c *Client) GetModuleContent(ctx context.Context, moduleID string) (ModuleContent, error) {
	var moduleContent ModuleContent
	if err := json.Unmarshal(stubJSON, &moduleContent); err != nil {
		return ModuleContent{}, fmt.Errorf("unmarshal stub: %w", err)
	}
	return moduleContent, nil
}
