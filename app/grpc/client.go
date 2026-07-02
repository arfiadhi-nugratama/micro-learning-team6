package grpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	apiv1 "github.com/Woven-dojo/ms1-proto/sdk-go/cmsbff/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	conn               *grpc.ClientConn
	svc                apiv1.CmsBffV1ServiceClient
	contentfulURL      string
	contentfulToken    string
	httpClient         *http.Client
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
			Sys     struct{ ID string `json:"id"` } `json:"sys"`
			Title   string                          `json:"ms1Title"`
			Body    string                          `json:"body"`
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

func (c *Client) GetModuleContent(ctx context.Context, moduleID, locale string) (string, error) {
	resp, err := c.svc.GetModuleNavTree(ctx, &apiv1.GetModuleNavTreeRequest{
		ModuleId: moduleID,
		Locale:   locale,
	})
	if err != nil {
		return "", fmt.Errorf("GetModuleNavTree: %w", err)
	}

	mod := resp.GetModule()
	if mod == nil {
		return "", fmt.Errorf("no module in response")
	}

	var sb strings.Builder
	sb.WriteString("Module: ")
	sb.WriteString(mod.GetTitle())
	sb.WriteString("\n\n")

	for _, unit := range mod.GetUnits() {
		sb.WriteString("Unit: ")
		sb.WriteString(unit.GetTitle())
		sb.WriteString("\n\n")

		for _, activity := range unit.GetActivities() {
			at := activity.GetActivityType()
			if at == "quiz" || at == "exercise" {
				continue
			}
			sb.WriteString("Activity: ")
			sb.WriteString(activity.GetTitle())
			sb.WriteString("\n\n")

			for _, concept := range activity.GetConcepts() {
				sb.WriteString("Concept: ")
				sb.WriteString(concept.GetTitle())
				sb.WriteString("\n")

				if c.contentfulURL != "" && c.contentfulToken != "" {
					body, err := c.fetchConceptBody(ctx, concept.GetSysId(), locale)
					if err == nil && body != "" {
						sb.WriteString(body)
						sb.WriteString("\n")
					}
				}
				sb.WriteString("\n")
			}
		}
	}

	return sb.String(), nil
}
