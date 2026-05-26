package sre

import (
	"context"
	"encoding/json"

	"github.com/jackc/pgx/v5"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/render-oss/render-mcp-server/pkg/client"
	"github.com/render-oss/render-mcp-server/pkg/pointers"
)

func renderQueryTool(c *client.ClientWithResponses) server.ServerTool {
	return server.ServerTool{
		Tool: mcp.NewTool("render_query",
			mcp.WithDescription(renderQueryDescription),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:           "Query Render Postgres",
				ReadOnlyHint:    pointers.From(true),
				DestructiveHint: pointers.From(false),
				IdempotentHint:  pointers.From(true),
			}),
			mcp.WithString("postgresId",
				mcp.Required(),
				mcp.Description("Render Postgres resource ID (e.g. dpg-abc123)"),
			),
			mcp.WithString("sql",
				mcp.Required(),
				mcp.Description("Read-only SQL query (SELECT, WITH, EXPLAIN)"),
			),
		),
		Handler: func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return handleRenderQuery(ctx, c, request)
		},
	}
}

func handleRenderQuery(ctx context.Context, c *client.ClientWithResponses, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	postgresId, err := request.RequireString("postgresId")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	sqlQuery, err := request.RequireString("sql")
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	resp, err := c.RetrievePostgresConnectionInfoWithResponse(ctx, postgresId)
	if err != nil {
		return mcp.NewToolResultError("Failed to get connection info: " + err.Error()), nil
	}
	if err := client.ErrorFromResponse(resp); err != nil {
		return mcp.NewToolResultError("Failed to get connection info: " + err.Error()), nil
	}
	if resp.JSON200 == nil {
		return mcp.NewToolResultError("No connection info returned"), nil
	}

	connString := resp.JSON200.ExternalConnectionString
	pgConfig, err := pgx.ParseConfig(connString)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("Error parsing connection string", err), nil
	}

	conn, err := pgx.ConnectConfig(ctx, pgConfig)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("Error connecting to database", err), nil
	}
	defer conn.Close(ctx)

	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return mcp.NewToolResultErrorFromErr("Error beginning transaction", err), nil
	}
	defer func() { _ = tx.Rollback(ctx) }()

	rows, err := tx.Query(ctx, sqlQuery)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("Error executing query", err), nil
	}
	defer rows.Close()

	fieldDescriptions := rows.FieldDescriptions()
	columnNames := make([]string, len(fieldDescriptions))
	for i, fd := range fieldDescriptions {
		columnNames[i] = string(fd.Name)
	}

	var results []map[string]interface{}
	for rows.Next() {
		values, err := rows.Values()
		if err != nil {
			return mcp.NewToolResultErrorFromErr("Error reading row values", err), nil
		}

		rowMap := make(map[string]interface{})
		for i, col := range columnNames {
			val := values[i]
			switch v := val.(type) {
			case []byte:
				rowMap[col] = string(v)
			default:
				rowMap[col] = v
			}
		}
		results = append(results, rowMap)
	}

	if err := rows.Err(); err != nil {
		return mcp.NewToolResultErrorFromErr("Error iterating rows", err), nil
	}

	respJSON, err := json.Marshal(results)
	if err != nil {
		return mcp.NewToolResultErrorFromErr("Error marshaling results", err), nil
	}

	return mcp.NewToolResultText(string(respJSON)), nil
}

const renderQueryDescription = `Run a read-only SQL query against a Render-hosted Postgres database. Write
operations (INSERT, UPDATE, DELETE, DROP, etc.) are rejected server-side.

Parameters:
  postgresId (string, required): Render Postgres resource ID (dpg- prefix).
    Get IDs from the render://inventory resource.
  sql (string, required): SQL query to execute. Must be read-only (SELECT, WITH,
    EXPLAIN). The server enforces this — write operations return an error.`
