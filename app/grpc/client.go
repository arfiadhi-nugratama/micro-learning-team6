package grpc

import (
	"context"
	"fmt"
	"strings"

	apiv1 "github.com/Woven-dojo/ms1-proto/sdk-go/cmsbff/api/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Client struct {
	conn   *grpc.ClientConn
	svc    apiv1.CmsBffV1ServiceClient
}

func NewClient(addr string) (*Client, error) {
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("grpc.NewClient: %w", err)
	}
	return &Client{
		conn: conn,
		svc:  apiv1.NewCmsBffV1ServiceClient(conn),
	}, nil
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
		sb.WriteString("\n")

		for _, activity := range unit.GetActivities() {
			at := activity.GetActivityType()
			if at == "quiz" || at == "exercise" {
				continue
			}
			sb.WriteString("  Activity: ")
			sb.WriteString(activity.GetTitle())
			sb.WriteString(" (")
			sb.WriteString(at)
			sb.WriteString(")\n")

			for _, concept := range activity.GetConcepts() {
				sb.WriteString("    Concept: ")
				sb.WriteString(concept.GetTitle())
				sb.WriteString("\n")
			}
		}
		sb.WriteString("\n")
	}

	return sb.String(), nil
}
