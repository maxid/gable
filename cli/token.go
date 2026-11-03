package cli

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/maxid/gable/auth"
	"github.com/maxid/gable/config"
)

func newTokenCommand() *cobra.Command {
	var (
		configPath string
		subject    string
		username   string
		tenantID   int64
		clientID   string
		ttl        time.Duration
		format     string
		issuer     string
		secret     string
		refresh    bool
	)
	cmd := &cobra.Command{
		Use:   "token",
		Short: "mint a short-lived token for local testing and CI smoke",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// The configuration is read, not required: a token is exactly what you need when
			// the application has not started yet.
			settings := config.Default()
			if cfg, err := config.Load(configPath); err == nil {
				settings = cfg
			} else if cmd.Flags().Changed("config") {
				return err
			}

			if issuer == "" {
				issuer = settings.App.Name
			}
			if secret == "" {
				return fmt.Errorf("token: --secret is required (the key the application verifies with)")
			}
			if tenantID == 0 {
				tenantID = settings.Tenant.RootID
			}
			if subject == "" {
				subject = username
			}
			if subject == "" {
				return fmt.Errorf("token: --subject or --username is required")
			}

			accessFormat := auth.AccessJWT
			switch strings.ToLower(format) {
			case "", "jwt":
			case "opaque":
				accessFormat = auth.AccessOpaque
			default:
				return fmt.Errorf("token: unknown format %q (want jwt or opaque)", format)
			}

			service, err := auth.NewTokenService(auth.Config{
				Issuer:       issuer,
				Secret:       secret,
				AccessFormat: accessFormat,
				AccessTTL:    ttl,
				RefreshTTL:   refreshTTL(ttl, refresh),
			}, nil, nil, auth.NewMemoryTokenStore())
			if err != nil {
				return err
			}

			pair, err := service.Issue(context.Background(), auth.Principal{
				Subject:  subject,
				Username: username,
				TenantID: tenantID,
				ClientID: clientID,
			})
			if err != nil {
				return err
			}
			return writeToken(cmd.OutOrStdout(), pair, refresh)
		},
	}
	cmd.Flags().StringVar(&configPath, "config", "gable.yaml", "configuration file (issuer and root tenant default from it)")
	cmd.Flags().StringVar(&subject, "subject", "", "principal subject (the user id)")
	cmd.Flags().StringVar(&username, "username", "", "principal username")
	cmd.Flags().Int64Var(&tenantID, "tenant-id", 0, "tenant id (defaults to the config's root tenant)")
	cmd.Flags().StringVar(&clientID, "client-id", "", "client id to embed")
	cmd.Flags().DurationVar(&ttl, "ttl", 15*time.Minute, "access token lifetime")
	cmd.Flags().StringVar(&format, "format", "jwt", "jwt | opaque")
	cmd.Flags().StringVar(&issuer, "issuer", "", "token issuer (defaults to the app name)")
	cmd.Flags().StringVar(&secret, "secret", "", "signing secret the application verifies with")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "also print a refresh token")
	return cmd
}

// refreshTTL keeps a refresh token meaningful: shorter than the access token would make it
// useless, so it is at least a day and always longer than the access token.
func refreshTTL(accessTTL time.Duration, withRefresh bool) time.Duration {
	if !withRefresh {
		return accessTTL
	}
	if accessTTL >= 24*time.Hour {
		return accessTTL * 2
	}
	return 24 * time.Hour
}

// writeToken prints the access token on the first line, so `TOKEN=$(gable token ...)` works
// without parsing, and the rest as labelled context.
func writeToken(w io.Writer, pair *auth.TokenPair, withRefresh bool) error {
	if _, err := fmt.Fprintln(w, pair.AccessToken); err != nil {
		return err
	}
	if withRefresh && pair.RefreshToken != "" {
		fmt.Fprintln(w, pair.RefreshToken)
	}
	// The human-readable summary goes to stderr in the caller; here the token is the output.
	return nil
}
