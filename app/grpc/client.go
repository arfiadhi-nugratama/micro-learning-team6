package grpc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	apiv1 "github.com/dojo-product/ms1-proto/sdk-go/cmsbff/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

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

func (c *Client) GetModuleContent(ctx context.Context, moduleID, locale string) (string, error) {
	// fetch EN tree always; fetch JA tree in parallel when locale is not already "ja"
	respEN, err := c.svc.GetModuleNavTree(ctx, &apiv1.GetModuleNavTreeRequest{
		ModuleId: moduleID,
		Locale:   "en",
	})
	if err != nil {
		return "", fmt.Errorf("GetModuleNavTree en: %w", err)
	}
	modEN := respEN.GetModule()
	if modEN == nil {
		return "", fmt.Errorf("no module in response")
	}

	// build a sysId→ja concept title/body lookup
	jaConceptTitle := map[string]string{}
	jaConceptBody := map[string]string{}

	respJA, err := c.svc.GetModuleNavTree(ctx, &apiv1.GetModuleNavTreeRequest{
		ModuleId: moduleID,
		Locale:   "ja",
	})
	if err == nil && respJA.GetModule() != nil {
		for _, unit := range respJA.GetModule().GetUnits() {
			for _, activity := range unit.GetActivities() {
				for _, concept := range activity.GetConcepts() {
					jaConceptTitle[concept.GetSysId()] = concept.GetTitle()
				}
			}
		}
		// fetch JA contentful bodies if configured
		if c.contentfulURL != "" && c.contentfulToken != "" {
			for id := range jaConceptTitle {
				body, ferr := c.fetchConceptBody(ctx, id, "ja")
				if ferr == nil && body != "" {
					jaConceptBody[id] = body
				}
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("Module: ")
	sb.WriteString(modEN.GetTitle())
	sb.WriteString("\n\n")

	for _, unit := range modEN.GetUnits() {
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
				id := concept.GetSysId()

				sb.WriteString("Concept-ID: ")
				sb.WriteString(id)
				sb.WriteString("\n")

				// EN block
				sb.WriteString("Concept-Title-EN: ")
				sb.WriteString(concept.GetTitle())
				sb.WriteString("\n")
				if c.contentfulURL != "" && c.contentfulToken != "" {
					body, ferr := c.fetchConceptBody(ctx, id, "en")
					if ferr == nil && body != "" {
						sb.WriteString("Concept-Body-EN: ")
						sb.WriteString(body)
						sb.WriteString("\n")
					}
				}

				// JA block
				if jaTitle, ok := jaConceptTitle[id]; ok {
					sb.WriteString("Concept-Title-JA: ")
					sb.WriteString(jaTitle)
					sb.WriteString("\n")
				}
				if jaBody, ok := jaConceptBody[id]; ok {
					sb.WriteString("Concept-Body-JA: ")
					sb.WriteString(jaBody)
					sb.WriteString("\n")
				}

				sb.WriteString("\n")
			}
		}
	}

	return sb.String(), nil
}
