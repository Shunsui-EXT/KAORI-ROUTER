package cmd

import (
	"context"
	"fmt"

	"github.com/Shunsui-EXT/KAORI-ROUTER/internal/config"
	sdkAuth "github.com/Shunsui-EXT/KAORI-ROUTER/sdk/auth"
	log "github.com/sirupsen/logrus"
)

// DoAlysisLogin triggers the Alysis Code Pro device-flow login and saves credentials.
func DoAlysisLogin(cfg *config.Config, options *LoginOptions) {
	if options == nil {
		options = &LoginOptions{}
	}

	manager := newAuthManager()
	authOpts := &sdkAuth.LoginOptions{
		NoBrowser:    options.NoBrowser,
		CallbackPort: options.CallbackPort,
		Metadata:     map[string]string{},
	}

	record, savedPath, err := manager.Login(context.Background(), "alysis", cfg, authOpts)
	if err != nil {
		log.Errorf("Alysis authentication failed: %v", err)
		return
	}

	if savedPath != "" {
		fmt.Printf("Authentication saved to %s\n", savedPath)
	}
	if record != nil && record.Label != "" {
		fmt.Printf("Authenticated as %s\n", record.Label)
	}
	fmt.Println("Alysis Code Pro authentication successful!")
}
