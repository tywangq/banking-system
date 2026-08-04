package api

import (
	"github.com/gin-gonic/gin"
	"github.com/tywangq/banking-system/health"
)

// The checks themselves live in package health, because the gRPC-Gateway mux has to
// serve the same two endpoints -- it is the server that actually listens in the
// deployed service. These wrappers only adapt them to Gin.

func (server *Server) live(ctx *gin.Context) {
	health.Live(ctx.Writer, ctx.Request)
}

func (server *Server) ready(ctx *gin.Context) {
	health.Ready(server.store)(ctx.Writer, ctx.Request)
}
