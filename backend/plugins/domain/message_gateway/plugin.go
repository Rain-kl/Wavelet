// Copyright 2026 Arctel.net
// SPDX-License-Identifier: Apache-2.0

// Package message_gateway provides the Bot gateway, multi-channel notification dispatching, and asynchronous push worker domain plugin for Cordis.
package message_gateway

import (
	"Wavelet/core"
	"Wavelet/core/contracts"
	"Wavelet/core/extpoints"
	"Wavelet/pkg/ginutil"
	"Wavelet/pkg/util"
	"Wavelet/plugins/domain/message_gateway/handler"
	"Wavelet/plugins/domain/message_gateway/model"
	"Wavelet/plugins/domain/message_gateway/repository"
	"Wavelet/plugins/domain/message_gateway/service"
	"context"
	"embed"
	"reflect"

	"github.com/gin-gonic/gin"
)

//go:embed migrations/*/*.sql
var mgMigrations embed.FS

// Option configures the message_gateway plugin.
type Option func(*Plugin)

// WithAutoStartRunner enables automatic bot runner startup in the background.
func WithAutoStartRunner(enable bool) Option {
	return func(p *Plugin) {
		p.autoStartRunner = enable
	}
}

// Plugin implements core.Plugin to provide Bot gateway and notification dispatch domain services.
type Plugin struct {
	autoStartRunner bool
	cancelRunner    context.CancelFunc
}

// New creates a new message_gateway domain plugin.
func New(opts ...Option) *Plugin {
	p := &Plugin{}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// Name returns the unique identifier for the message_gateway domain plugin.
func (p *Plugin) Name() string {
	return "message_gateway"
}

// Inject declares required dependencies for the message_gateway domain plugin.
func (p *Plugin) Inject() []reflect.Type {
	return []reflect.Type{
		reflect.TypeFor[contracts.DBService](),
		// AuthService is captured as a middleware value in Apply, so it cannot
		// be late-bound with core.When like the other services below; the
		// kernel must mount auth first or the routes get a pass-through guard.
		reflect.TypeFor[contracts.AuthService](),
	}
}

// Manifest returns the plugin metadata.
func (p *Plugin) Manifest() core.Manifest {
	return core.Manifest{
		Name:        "message_gateway",
		Version:     "1.0.0",
		Description: "Bot gateway, multi-channel notification push, and async worker dispatch plugin",
		Author:      "Wavelet Team",
	}
}

type mgAppConfig struct {
	SessionSecret string `config:"session_secret" env:"APP_SESSION_SECRET" secret:"true"`
}

// DeclareConfig declares configuration bindings for the message_gateway plugin.
func (p *Plugin) DeclareConfig() []core.ConfigBinding {
	return []core.ConfigBinding{
		{Prefix: "app", Target: &mgAppConfig{}},
	}
}

// Apply registers message_gateway migrations, routes, tasks, schedules, events, and settings into the Context.
func (p *Plugin) Apply(ctx *core.Context) error {
	var cfg mgAppConfig
	if err := ctx.Config().Bind("app", &cfg); err == nil && cfg.SessionSecret != "" {
		service.SetCredentialSecret(cfg.SessionSecret)
	}
	// 0. Bind DBService, CacheService, TaskService, UserService
	if db, err := core.Inject[contracts.DBService](ctx); err == nil && db != nil {
		repository.SetDBService(db)
	} else {
		core.When[contracts.DBService](ctx, func(db contracts.DBService) {
			repository.SetDBService(db)
		})
	}
	if cache, err := core.Inject[contracts.CacheService](ctx); err == nil && cache != nil {
		repository.SetCacheService(cache)
		service.SetCacheService(cache)
	} else {
		core.When[contracts.CacheService](ctx, func(cache contracts.CacheService) {
			repository.SetCacheService(cache)
			service.SetCacheService(cache)
		})
	}
	if taskSvc, err := core.Inject[contracts.TaskService](ctx); err == nil && taskSvc != nil {
		service.SetTaskService(taskSvc)
	} else {
		core.When[contracts.TaskService](ctx, func(taskSvc contracts.TaskService) {
			service.SetTaskService(taskSvc)
		})
	}
	if uSvc, err := core.Inject[contracts.UserService](ctx); err == nil && uSvc != nil {
		service.SetUserService(uSvc)
	} else {
		core.When[contracts.UserService](ctx, func(uSvc contracts.UserService) {
			service.SetUserService(uSvc)
		})
	}
	ctx.OnDispose(func() error {
		repository.SetDBService(nil)
		repository.SetCacheService(nil)
		service.SetCacheService(nil)
		service.SetTaskService(nil)
		service.SetUserService(nil)
		return nil
	})

	// 0. Resolve auth service for middleware (via IoC, not direct import)
	denyAuth := ginutil.AuthUnavailable()
	loginMW := denyAuth
	adminMW := denyAuth
	if authSvc, err := core.Inject[contracts.AuthService](ctx); err == nil && authSvc != nil {
		if mw, ok := authSvc.RequireAuthMiddleware().(gin.HandlerFunc); ok {
			loginMW = mw
		}
		if mw, ok := authSvc.RequireAdminMiddleware().(gin.HandlerFunc); ok {
			adminMW = mw
		}
	}

	// 1. Register migrations
	ctx.Migrations().Register("message_gateway", mgMigrations)

	// 2. Register User HTTP Routes
	handler.RegisterUserRoutes(ctx.Router().Group("/api/v1"), loginMW)

	// 3. Register Admin Message Gateway HTTP Routes
	handler.RegisterAdminRoutes(ctx.Router().Group("/api/v1/admin"), loginMW, adminMW)

	// 4. Register Admin Push HTTP Routes
	handler.RegisterAdminPushRoutes(ctx.Router().Group("/api/v1/admin"), loginMW, adminMW)

	const defaultTaskRetry = 3
	pushHandler := &service.PushHandler{}

	// 5. Register background tasks
	ctx.Task().Register("message_gateway:push_notification", func(c context.Context, payload []byte) error {
		return pushHandler.Execute(c, payload)
	}, extpoints.WithTaskRetry(defaultTaskRetry))

	ctx.Task().Register(service.SendNotificationTask, func(c context.Context, payload []byte) error {
		return pushHandler.Execute(c, payload)
	}, extpoints.WithTaskRetry(defaultTaskRetry))

	ctx.Task().Register("message_gateway:dispatch_bot_msg", func(_ context.Context, _ []byte) error {
		return nil
	})

	ctx.Task().Register("message_gateway:cleanup_pairing_codes", func(c context.Context, _ []byte) error {
		return repository.DeleteExpiredPairingCodes(c)
	}, extpoints.WithTaskRetry(defaultTaskRetry))

	// 6. Register Cron Schedules
	ctx.Schedule().RegisterCron("*/10 * * * *", "message_gateway:cleanup_pairing_codes", map[string]any{"action": "cleanup"})

	// 7. Register EventBus listeners for decoupled push triggers
	ctx.Events().On("notification:push", func(c context.Context, e model.PushNotificationEvent) error {
		meta := model.EventMetadata{
			Key:  "eventbus:" + e.Channel,
			Name: e.Title,
			DefaultTemplate: model.NotificationMessage{
				Title:   e.Title,
				Content: e.Content,
				Level:   model.DefaultLevelInfo,
				Ext:     e.Metadata,
			},
			Description: "EventBus triggered notification",
		}
		service.DefaultTrigger.Trigger(c, meta, map[string]any{
			"user.id": e.UserID,
			"title":   e.Title,
			"content": e.Content,
		})
		return nil
	})

	// 8. Register task completed event listener
	ctx.Events().On(contracts.EventTopicTaskCompleted, func(c context.Context, e contracts.TaskCompletedEvent) error {
		service.HandleTaskCompleted(c, e)
		return nil
	})

	// 9. Register built-in domain events
	service.RegisterCustomEvents()

	// 10. Register Settings Schemas
	ctx.Settings().Register(extpoints.SettingSchema{
		Key:         "message_gateway.pairing_code_expiry_minutes",
		Default:     15,
		Description: "Expiry duration for bot pairing codes in minutes",
		Type:        "integer",
		Category:    "messaging",
	})
	ctx.Settings().Register(extpoints.SettingSchema{
		Key:         "message_gateway.max_bindings_per_user",
		Default:     5,
		Description: "Maximum platform bot bindings per user",
		Type:        "integer",
		Category:    "messaging",
	})

	// 11. Optional runner start & lifecycle
	if p.autoStartRunner {
		runnerCtx, cancel := context.WithCancel(ctx.GoContext())
		p.cancelRunner = cancel
		util.Go(func() {
			_ = service.Start(runnerCtx)
		})
	}

	ctx.OnDispose(func() error {
		if p.cancelRunner != nil {
			p.cancelRunner()
		}
		return nil
	})

	return nil
}

// Re-exported constants.
const (
	CodeAlphabet = service.CodeAlphabet
	CodeLength   = service.CodeLength
)

// MessageChannel is an alias for model.MessageChannel.
type MessageChannel = model.MessageChannel

// MessageBinding is an alias for model.MessageBinding.
type MessageBinding = model.MessageBinding

// MessagePairingCode is an alias for model.MessagePairingCode.
type MessagePairingCode = model.MessagePairingCode

// PushChannel is an alias for model.PushChannel.
type PushChannel = model.PushChannel

// PushEvent is an alias for model.PushEvent.
type PushEvent = model.PushEvent

// PushHistory is an alias for model.PushHistory.
type PushHistory = model.PushHistory

// PushNotificationEvent is an alias for model.PushNotificationEvent.
type PushNotificationEvent = model.PushNotificationEvent

// ChannelConfig is an alias for model.ChannelConfig.
type ChannelConfig = model.ChannelConfig

// Capability is an alias for model.Capability.
type Capability = model.Capability

// Recipient is an alias for model.Recipient.
type Recipient = model.Recipient

// Attachment is an alias for model.Attachment.
type Attachment = model.Attachment

// InboundMessage is an alias for model.InboundMessage.
type InboundMessage = model.InboundMessage

// OutboundMessage is an alias for model.OutboundMessage.
type OutboundMessage = model.OutboundMessage

// BindingDTO is an alias for model.BindingDTO.
type BindingDTO = model.BindingDTO

// PublicChannelDTO is an alias for model.PublicChannelDTO.
type PublicChannelDTO = model.PublicChannelDTO

// Definition is an alias for model.Definition.
type Definition = model.Definition

// ChannelDTO is an alias for model.ChannelDTO.
type ChannelDTO = model.ChannelDTO

// CreateChannelRequest is an alias for model.CreateChannelRequest.
type CreateChannelRequest = model.CreateChannelRequest

// UpdateChannelRequest is an alias for model.UpdateChannelRequest.
type UpdateChannelRequest = model.UpdateChannelRequest

// PushDefinition is an alias for model.PushDefinition.
type PushDefinition = model.PushDefinition

// PushField is an alias for model.PushField.
type PushField = model.PushField

// NotificationMessage is an alias for model.NotificationMessage.
type NotificationMessage = model.NotificationMessage

// EventMetadata is an alias for model.EventMetadata.
type EventMetadata = model.EventMetadata

// SendPayload is an alias for model.SendPayload.
type SendPayload = model.SendPayload

// Handler is an alias for service.Handler.
type Handler = service.Handler

// Factory is an alias for service.Factory.
type Factory = service.Factory

// Channel is an alias for service.Channel.
type Channel = service.Channel

// Runner is an alias for service.Runner.
type Runner = service.Runner

// EventTrigger is an alias for service.EventTrigger.
type EventTrigger = service.EventTrigger

// PushHandler is an alias for service.PushHandler.
type PushHandler = service.PushHandler

// Re-exported variables and functions.
var (
	SetDBServiceForTest = repository.SetDBServiceForTest
	UpsertPairingCode   = repository.UpsertPairingCode
	Register            = service.Register
	Lookup              = service.Lookup
	GenerateCode        = service.GenerateCode
	NormalizeCode       = service.NormalizeCode
	FormatCode          = service.FormatCode
	Start               = service.Start
	Stop                = service.Stop
	GlobalRunner        = service.GlobalRunner
	DefaultTrigger      = service.DefaultTrigger
	SyncEvents          = service.SyncEvents
	AdminLogin          = service.AdminLogin
	HandleAdminLoggedIn = service.HandleAdminLoggedIn
)
