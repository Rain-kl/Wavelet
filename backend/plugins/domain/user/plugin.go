// Copyright 2026 Arctel.net
// SPDX-License-Identifier: Apache-2.0

// Package user provides the user profile, credential management, role management, and access token domain plugin for Cordis.
package user

import (
	"Wavelet/core"
	"Wavelet/core/contracts"
	"Wavelet/core/extpoints"
	"Wavelet/pkg/ginutil"
	"context"
	"embed"
	"reflect"

	"github.com/gin-gonic/gin"
)

//go:embed migrations/*/*.sql
var userMigrations embed.FS

// Option configures the user plugin.
type Option func(*Plugin)

// WithUserService sets a custom UserService implementation.
func WithUserService(svc contracts.UserService) Option {
	return func(p *Plugin) {
		p.userSvc = svc
	}
}

// Plugin implements core.Plugin to provide user account and credential domain services.
type Plugin struct {
	userSvc contracts.UserService
}

// New creates a new user domain plugin.
func New(opts ...Option) *Plugin {
	p := &Plugin{}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// PluginName 用户插件唯一名称标识
const PluginName = "user"

// Name returns the unique identifier for the user domain plugin.
func (p *Plugin) Name() string {
	return PluginName
}

// Inject declares required dependencies for the user domain plugin.
func (p *Plugin) Inject() []reflect.Type {
	return []reflect.Type{
		reflect.TypeFor[contracts.DBService](),
		// Apply resolves AuthService to build the route auth middleware. The
		// kernel only gates Apply on declared deps, so leaving this out lets
		// user mount before auth and fall back to a pass-through middleware.
		reflect.TypeFor[contracts.AuthService](),
	}
}

// Manifest returns the plugin metadata.
func (p *Plugin) Manifest() core.Manifest {
	return core.Manifest{
		Name:        PluginName,
		Version:     "1.0.0",
		Description: "User profiles, credentials, role management, and access token domain plugin",
		Author:      "Wavelet Team",
	}
}

// Apply registers user migrations, services, routes, tasks, schedules, and settings into the Context.
func (p *Plugin) Apply(ctx *core.Context) error {
	// 0. Bind DBService from Context
	if db, err := core.Inject[contracts.DBService](ctx); err == nil && db != nil {
		SetDBService(db)
	} else {
		core.When[contracts.DBService](ctx, func(db contracts.DBService) {
			SetDBService(db)
		})
	}
	ctx.OnDispose(func() error {
		SetDBService(nil)
		return nil
	})

	// 0.1 Resolve auth service for middleware (via IoC, not direct import)
	denyAuth := ginutil.AuthUnavailable()
	loginMW := denyAuth
	noTokenMW := denyAuth
	if authSvc, err := core.Inject[contracts.AuthService](ctx); err == nil && authSvc != nil {
		if mw, ok := authSvc.RequireAuthMiddleware().(gin.HandlerFunc); ok {
			loginMW = mw
		}
		if mw, ok := authSvc.DisallowTokenAuthMiddleware().(gin.HandlerFunc); ok {
			noTokenMW = mw
		}
	}

	// 1. Register migrations
	ctx.Migrations().Register("user", userMigrations)

	// 2. Initialize and provide UserService
	if p.userSvc == nil {
		p.userSvc = newUserService(ctx.Events())
	}
	core.Provide[contracts.UserService](ctx, p.userSvc)

	// 3. Register HTTP Routes
	userGroup := ctx.Router().Group("/api/v1/user")
	{
		userGroup.POST("/login", Login)
		userGroup.POST("/register", Register)
		userGroup.GET("/logout", Logout)
		userGroup.POST("/send-email-code", SendEmailCode)
		userGroup.POST("/change-password", loginMW, ChangePassword)
		userGroup.PUT("/profile", loginMW, UpdateProfile)

		// Access Tokens
		tokensGroup := userGroup.Group("/access-tokens", loginMW, noTokenMW)
		{
			tokensGroup.GET("", ListAccessTokens)
			tokensGroup.POST("", CreateAccessToken)
			tokensGroup.DELETE("/:id", DeleteAccessToken)
			tokensGroup.POST("/:id/rotate", RotateAccessToken)
		}
	}

	const defaultUserTaskRetry = 3

	// 4. Register background tasks
	ctx.Task().Register("user:send_email_code", func(_ context.Context, _ []byte) error {
		return nil
	}, extpoints.WithTaskRetry(defaultUserTaskRetry))

	ctx.Task().Register("user:cleanup_inactive", func(_ context.Context, _ []byte) error {
		return nil
	})

	// 5. Register Settings Schemas
	ctx.Settings().Register(extpoints.SettingSchema{
		Key:         "user.registration_enabled",
		Default:     true,
		Description: "Whether new user registration is enabled",
		Type:        "boolean",
		Category:    "general",
		Public:      true,
	})
	ctx.Settings().Register(extpoints.SettingSchema{
		Key:         "user.password_login_enabled",
		Default:     true,
		Description: "Whether password login is enabled",
		Type:        "boolean",
		Category:    "general",
		Public:      true,
	})
	ctx.Settings().Register(extpoints.SettingSchema{
		Key:         "user.min_password_length",
		Default:     8,
		Description: "Minimum password length required for user accounts",
		Type:        "integer",
		Category:    "security",
	})

	return nil
}
