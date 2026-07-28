package main

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

const (
	maxRequestBodyBytes = 64 << 20
)

type logRouterStore interface {
	LogStore
	MetricsStore
	BackgroundError() error
}

type errorResponse struct {
	Error string `json:"error"`
}

func NewRouter(database logRouterStore) *gin.Engine {
	if database == nil {
		panic("router: nil store")
	}

	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())
	router.HandleMethodNotAllowed = true

	router.GET("/healthz", func(c *gin.Context) {
		if err := database.BackgroundError(); err != nil {
			c.JSON(http.StatusServiceUnavailable, errorResponse{Error: "store background maintenance failed"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	registerLogRoutes(router, database)
	registerMetricsRoute(router, database)

	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, errorResponse{Error: "route not found"})
	})
	router.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, errorResponse{Error: "method not allowed"})
	})
	return router
}

func requestErrorStatus(err error) int {
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		return http.StatusRequestEntityTooLarge
	}
	return http.StatusBadRequest
}

func writeJSONError(c *gin.Context, status int, message string) {
	c.AbortWithStatusJSON(status, errorResponse{Error: message})
}
