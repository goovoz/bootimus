package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"bootimus/internal/admin"
	"bootimus/internal/server"

	"github.com/spf13/cobra"
)

var openapiCmd = &cobra.Command{
	Use:   "openapi",
	Short: "Print the OpenAPI document for the REST API",
	Long:  `Print the OpenAPI 3.0 document describing the admin and boot HTTP endpoints as JSON on stdout.`,
	Run: func(cmd *cobra.Command, args []string) {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(admin.BuildOpenAPI(server.Version, "")); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write OpenAPI document: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(openapiCmd)
}
