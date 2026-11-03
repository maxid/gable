package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/maxid/gable/mcp"
)

func newMCPCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "talk to a remote MCP server",
	}
	cmd.AddCommand(newMCPListCommand(), newMCPCallCommand())
	return cmd
}

// mcpEndpoint turns the two flags into one endpoint and validates the choice.
//
// The flags are separate because they mean different things — a URL is somebody else's server,
// a command is one gable starts — and a typo in either should fail before a connection.
func mcpEndpoint(url, command string, args []string) (mcp.Endpoint, error) {
	switch {
	case url != "" && command != "":
		return mcp.Endpoint{}, fmt.Errorf("mcp: pass --url or --command, not both")
	case url == "" && command == "":
		return mcp.Endpoint{}, fmt.Errorf("mcp: one of --url or --command is required")
	case url != "":
		return mcp.Endpoint{URL: url}, nil
	}
	parts := append([]string{command}, args...)
	return mcp.Endpoint{Command: parts}, nil
}

func newMCPListCommand() *cobra.Command {
	var url, command, format string
	var args []string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list the tools a remote server exposes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			endpoint, err := mcpEndpoint(url, command, args)
			if err != nil {
				return err
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()

			client, err := mcp.Dial(ctx, endpoint)
			if err != nil {
				return err
			}
			defer client.Close()

			tools, err := client.ListTools(ctx)
			if err != nil {
				return err
			}
			if strings.EqualFold(format, "json") {
				raw, err := json.MarshalIndent(tools, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), string(raw))
				return nil
			}
			if len(tools) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "no tools")
				return nil
			}
			for _, tool := range tools {
				description := strings.TrimSpace(tool.Description)
				if description == "" {
					description = "-"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\n", tool.Name, description)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "streamable-HTTP endpoint of the server")
	cmd.Flags().StringVar(&command, "command", "", "executable that speaks MCP over stdio")
	cmd.Flags().StringArrayVar(&args, "arg", nil, "argument for --command, repeatable")
	cmd.Flags().StringVar(&format, "format", "text", "text | json")
	return cmd
}

func newMCPCallCommand() *cobra.Command {
	var url, command, arguments, format string
	var args []string
	cmd := &cobra.Command{
		Use:   "call <tool>",
		Short: "call a tool on a remote server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, toolArgs []string) error {
			endpoint, err := mcpEndpoint(url, command, args)
			if err != nil {
				return err
			}
			payload := json.RawMessage(`{}`)
			if strings.TrimSpace(arguments) != "" {
				payload = json.RawMessage(arguments)
				if !json.Valid(payload) {
					return fmt.Errorf("mcp: --arguments is not valid JSON")
				}
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 60*time.Second)
			defer cancel()

			client, err := mcp.Dial(ctx, endpoint)
			if err != nil {
				return err
			}
			defer client.Close()

			result, err := client.CallTool(ctx, toolArgs[0], payload)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if strings.EqualFold(format, "json") {
				raw, err := json.MarshalIndent(result, "", "  ")
				if err != nil {
					return err
				}
				fmt.Fprintln(out, string(raw))
			} else if text := mcp.ResponseText(result); text != "" {
				fmt.Fprintln(out, text)
			}
			// An IsError result is the server saying the tool failed. Print what it said and exit
			// non-zero, so a script gets the same verdict a human does.
			if result.IsError {
				return fmt.Errorf("mcp: tool %q reported an error", toolArgs[0])
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&url, "url", "", "streamable-HTTP endpoint of the server")
	cmd.Flags().StringVar(&command, "command", "", "executable that speaks MCP over stdio")
	cmd.Flags().StringArrayVar(&args, "arg", nil, "argument for --command, repeatable")
	cmd.Flags().StringVar(&arguments, "arguments", "{}", "tool arguments as JSON")
	cmd.Flags().StringVar(&format, "format", "text", "text | json")
	return cmd
}
