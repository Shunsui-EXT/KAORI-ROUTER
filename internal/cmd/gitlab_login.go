package cmd

import (
	"context"
	"fmt"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	sdkAuth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/auth"
	log "github.com/sirupsen/logrus"
)

// DoGitLabLogin triggers the GitLab Duo OAuth (PKCE) login flow and saves credentials.
func DoGitLabLogin(cfg *config.Config, options *LoginOptions) {
	if options == nil {
		options = &LoginOptions{}
	}

	promptFn := options.Prompt
	if promptFn == nil {
		promptFn = defaultProjectPrompt()
	}

	manager := newAuthManager()
	authOpts := &sdkAuth.LoginOptions{
		NoBrowser:    options.NoBrowser,
		CallbackPort: options.CallbackPort,
		Metadata: map[string]string{
			"login_mode": "oauth",
		},
		Prompt: promptFn,
	}

	record, savedPath, err := manager.Login(context.Background(), "gitlab", cfg, authOpts)
	if err != nil {
		log.Errorf("GitLab Duo authentication failed: %v", err)
		return
	}

	if savedPath != "" {
		fmt.Printf("Authentication saved to %s\n", savedPath)
	}
	if record != nil && record.Label != "" {
		fmt.Printf("Authenticated as %s\n", record.Label)
	}
	fmt.Println("GitLab Duo authentication successful!")
}

// DoGitLabTokenLogin triggers the GitLab Duo personal-access-token login flow and saves credentials.
func DoGitLabTokenLogin(cfg *config.Config, options *LoginOptions) {
	if options == nil {
		options = &LoginOptions{}
	}

	promptFn := options.Prompt
	if promptFn == nil {
		promptFn = defaultProjectPrompt()
	}

	manager := newAuthManager()
	authOpts := &sdkAuth.LoginOptions{
		Metadata: map[string]string{
			"login_mode": "pat",
		},
		Prompt: promptFn,
	}

	record, savedPath, err := manager.Login(context.Background(), "gitlab", cfg, authOpts)
	if err != nil {
		log.Errorf("GitLab Duo PAT authentication failed: %v", err)
		return
	}

	if savedPath != "" {
		fmt.Printf("Authentication saved to %s\n", savedPath)
	}
	if record != nil && record.Label != "" {
		fmt.Printf("Authenticated as %s\n", record.Label)
	}
	fmt.Println("GitLab Duo PAT authentication successful!")
}
