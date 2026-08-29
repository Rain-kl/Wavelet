// Copyright 2026 Arctel.net
// SPDX-License-Identifier: Apache-2.0

package ginutil

import (
	"Wavelet/pkg/response"

	"github.com/gin-gonic/gin"
)

// errAuthUnavailable is reported when a route's authentication guard cannot be
// resolved, so the request is rejected instead of reaching the handler.
const errAuthUnavailable = "authServiceUnavailable"

// AuthUnavailable returns a middleware that denies the request. Plugins use it
// as the fallback when contracts.AuthService cannot be resolved or its middleware
// has an unexpected shape: the alternative is a pass-through closure that serves
// the request as if it were authenticated.
func AuthUnavailable() gin.HandlerFunc {
	return func(c *gin.Context) {
		response.AbortUnauthorized(c, errAuthUnavailable)
	}
}
