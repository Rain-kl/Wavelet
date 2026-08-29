// Copyright 2026 Arctel.net
// SPDX-License-Identifier: Apache-2.0

// Package admin provides the system management console, diagnostics, audit logging, and configuration hot-reloading domain plugin for Cordis.
package admin

import (
	"Wavelet/core"
	"Wavelet/core/contracts"
	"Wavelet/core/extpoints"
	"Wavelet/pkg/ginutil"
	"Wavelet/plugins/domain/admin/handler"
	"Wavelet/plugins/domain/admin/model"
	"Wavelet/plugins/domain/admin/service"
	"context"
	"embed"
	"reflect"

	"github.com/gin-gonic/gin"
)

// SystemConfig aliases model.SystemConfig for external compatibility.
type SystemConfig = model.SystemConfig

//go:embed migrations/*/*.sql
var adminMigrations embed.FS

// Option configures the admin plugin.
type Option func(*Plugin)

// Plugin implements core.Plugin to provide system administration and management APIs.
type Plugin struct{}

// New creates a new admin domain plugin.
func New(opts ...Option) *Plugin {
	p := &Plugin{}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// Name returns the unique identifier for the admin domain plugin.
func (p *Plugin) Name() string {
	return "admin"
}

// Inject declares required dependencies for the admin domain plugin.
func (p *Plugin) Inject() []reflect.Type {
	return []reflect.Type{
		reflect.TypeFor[contracts.DBService](),
		reflect.TypeFor[contracts.CacheService](),
		reflect.TypeFor[contracts.UserService](),
		reflect.TypeFor[contracts.AuthService](),
	}
}

// Manifest returns the plugin metadata.
func (p *Plugin) Manifest() core.Manifest {
	return core.Manifest{
		Name:        "admin",
		Version:     "1.0.0",
		Description: "System administration console, diagnostic monitoring, and configuration hot-reload plugin",
		Author:      "Wavelet Team",
	}
}

// DeclareConfig declares configuration bindings consumed by the admin plugin.
func (p *Plugin) DeclareConfig() []core.ConfigBinding {
	return []core.ConfigBinding{
		{Prefix: "database", Target: &model.DatabaseConfig{}},
		{Prefix: "clickhouse", Target: &model.ClickHouseConfig{}},
	}
}

// Apply registers admin routes, tasks, schedules, and settings into the Context.
func (p *Plugin) Apply(ctx *core.Context) error {
	var dbCfg model.DatabaseConfig
	_ = ctx.Config().Bind("database", &dbCfg)
	service.SetDBConfig(dbCfg)

	var chCfg model.ClickHouseConfig
	_ = ctx.Config().Bind("clickhouse", &chCfg)
	service.SetClickHouseConfig(chCfg)

	// 0. Bind Services reactively
	if db, err := core.Inject[contracts.DBService](ctx); err == nil && db != nil {
		service.SetDBService(db)
	} else {
		core.When[contracts.DBService](ctx, func(db contracts.DBService) {
			service.SetDBService(db)
		})
	}
	if cache, err := core.Inject[contracts.CacheService](ctx); err == nil && cache != nil {
		service.SetCacheService(cache)
	} else {
		core.When[contracts.CacheService](ctx, func(cache contracts.CacheService) {
			service.SetCacheService(cache)
		})
	}
	if user, err := core.Inject[contracts.UserService](ctx); err == nil && user != nil {
		service.SetUserService(user)
	} else {
		core.When[contracts.UserService](ctx, func(user contracts.UserService) {
			service.SetUserService(user)
		})
	}
	if auth, err := core.Inject[contracts.AuthService](ctx); err == nil && auth != nil {
		service.SetAuthService(auth)
	} else {
		core.When[contracts.AuthService](ctx, func(auth contracts.AuthService) {
			service.SetAuthService(auth)
		})
	}
	if task, err := core.Inject[contracts.TaskService](ctx); err == nil && task != nil {
		service.SetTaskService(task)
	} else {
		core.When[contracts.TaskService](ctx, func(task contracts.TaskService) {
			service.SetTaskService(task)
		})
	}
	if storage, err := core.Inject[contracts.StorageService](ctx); err == nil && storage != nil {
		service.SetStorageService(storage)
	} else {
		core.When[contracts.StorageService](ctx, func(storage contracts.StorageService) {
			service.SetStorageService(storage)
		})
	}
	if rc, err := core.Inject[contracts.RiskControlService](ctx); err == nil && rc != nil {
		service.SetRiskControlService(rc)
	} else {
		core.When[contracts.RiskControlService](ctx, func(rc contracts.RiskControlService) {
			service.SetRiskControlService(rc)
		})
	}
	service.SetEventEmitter(ctx.Events().Emit)

	ctx.OnDispose(func() error {
		service.ResetServices()
		return nil
	})

	// 0a. Dynamic Auth Middlewares
	denyAuth := ginutil.AuthUnavailable()
	var loginMW gin.HandlerFunc = func(c *gin.Context) {
		if authSvc := service.GetAuthService(c.Request.Context()); authSvc != nil {
			if mw, ok := authSvc.RequireAuthMiddleware().(gin.HandlerFunc); ok {
				mw(c)
				return
			}
		}
		denyAuth(c)
	}
	var adminMW gin.HandlerFunc = func(c *gin.Context) {
		if authSvc := service.GetAuthService(c.Request.Context()); authSvc != nil {
			if mw, ok := authSvc.RequireAdminMiddleware().(gin.HandlerFunc); ok {
				mw(c)
				return
			}
		}
		denyAuth(c)
	}

	// 0b. Register migrations
	ctx.Migrations().Register("admin", adminMigrations)

	// 1. Register Admin HTTP Routes
	adminRouter := ctx.Router().Group("/api/v1/admin", loginMW, adminMW)
	handler.RegisterRoutes(adminRouter)

	// 2. Register Background Tasks
	ctx.Task().Register("admin:system_cleanup", func(_ context.Context, _ []byte) error {
		return nil
	}, extpoints.WithTaskRetry(1))

	// 3. Register Cron Schedules
	ctx.Schedule().RegisterCron("0 4 * * *", "admin:system_cleanup", map[string]string{"type": "daily"})

	// 4. Register Settings Schemas
	ctx.Settings().Register(extpoints.SettingSchema{
		Key:         "admin.system_cleanup_cron",
		Default:     "0 4 * * *",
		Description: "Cron expression for nightly system logs and expired tokens cleanup",
		Type:        "string",
		Category:    "maintenance",
	})

	return nil
}
