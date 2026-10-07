package http

import (
	"context"
	"encoding/base64"
	"fmt"
	"html/template"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"omniroute-api-wa/internal/config"
	"omniroute-api-wa/internal/repository/postgres"
	"omniroute-api-wa/internal/service"
)

// Handler handles HTTP requests for monitoring and WhatsApp pairing.
type Handler struct {
	cfg       *config.Config
	chatRepo  postgres.ChatRepository
	gowaSvc   service.GoWAService
	startTime time.Time
	
	// HTML templates
	alreadyLoggedInTemplate *template.Template
	waitingForQRTemplate    *template.Template
	qrScannerTemplate       *template.Template
}

// NewHandler creates a new HTTP handler instance.
func NewHandler(cfg *config.Config, chatRepo postgres.ChatRepository, gowaSvc service.GoWAService) *Handler {
	// Parse HTML templates
	alreadyLoggedInTmpl, err := template.ParseFiles(
		"internal/delivery/http/templates/already_logged_in.html",
	)
	if err != nil {
		panic(fmt.Sprintf("failed to parse already_logged_in template: %v", err))
	}
	
	waitingForQRTmpl, err := template.ParseFiles(
		"internal/delivery/http/templates/waiting_for_qr.html",
	)
	if err != nil {
		panic(fmt.Sprintf("failed to parse waiting_for_qr template: %v", err))
	}
	
	qrsScannerTmpl, err := template.ParseFiles(
		"internal/delivery/http/templates/qr_scanner.html",
	)
	if err != nil {
		panic(fmt.Sprintf("failed to parse qr_scanner template: %v", err))
	}
	
	return &Handler{
		cfg:                   cfg,
		chatRepo:              chatRepo,
		gowaSvc:               gowaSvc,
		startTime:             time.Now(),
		alreadyLoggedInTemplate: alreadyLoggedInTmpl,
		waitingForQRTemplate:    waitingForQRTmpl,
		qrScannerTemplate:       qrsScannerTmpl,
	}
}

// RegisterRoutes registers endpoints to the Gin engine.
func (h *Handler) RegisterRoutes(r *gin.Engine) {
	r.GET("/", h.Index)
	r.GET("/health", h.Health)
	r.GET("/status", h.Status)
	r.GET("/qr", h.QR)
	r.GET("/qr/image", h.QRImage)
}

// Index redirects root to status or qr page.
func (h *Handler) Index(c *gin.Context) {
	status := h.gowaSvc.GetStatus()
	if status.IsLoggedIn {
		c.Redirect(http.StatusTemporaryRedirect, "/status")
		return
	}
	c.Redirect(http.StatusTemporaryRedirect, "/qr")
}

// Health checks the operational health of PostgreSQL and the WhatsApp socket.
func (h *Handler) Health(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	dbHealthy := true
	if err := h.chatRepo.Ping(ctx); err != nil {
		dbHealthy = false
	}

	waStatus := h.gowaSvc.GetStatus()

	httpStatus := http.StatusOK
	statusText := "HEALTHY"
	if !dbHealthy {
		httpStatus = http.StatusServiceUnavailable
		statusText = "DEGRADED"
	}

	c.JSON(httpStatus, gin.H{
		"status":    statusText,
		"uptime":    time.Since(h.startTime).String(),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"components": gin.H{
			"database": gin.H{
				"status": map[bool]string{true: "UP", false: "DOWN"}[dbHealthy],
			},
			"whatsapp": gin.H{
				"connected":  waStatus.IsConnected,
				"logged_in":  waStatus.IsLoggedIn,
				"phone_user": waStatus.PhoneNumber,
			},
		},
	})
}

// Status returns comprehensive JSON runtime details.
func (h *Handler) Status(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 3*time.Second)
	defer cancel()

	dbStatus := "OK"
	if err := h.chatRepo.Ping(ctx); err != nil {
		dbStatus = fmt.Sprintf("Error: %v", err)
	}

	waStatus := h.gowaSvc.GetStatus()

	c.JSON(http.StatusOK, gin.H{
		"app": gin.H{
			"name":        "omniroute-api-wa",
			"uptime":      time.Since(h.startTime).String(),
			"started_at":  h.startTime.Format(time.RFC3339),
			"environment": h.cfg.GinMode,
		},
		"database": gin.H{
			"host":   h.cfg.DBHost,
			"port":   h.cfg.DBPort,
			"name":   h.cfg.DBName,
			"status": dbStatus,
		},
		"whatsapp": waStatus,
		"omniroute": gin.H{
			"base_url":             h.cfg.OmnirouteAPIBaseURL,
			"model":                h.cfg.OmnirouteModel,
			"max_context_messages": h.cfg.MaxContextMessages,
		},
	})
}

// QR serves the WhatsApp login QR code in HTML, PNG, or JSON depending on headers/query.
func (h *Handler) QR(c *gin.Context) {
	format := c.DefaultQuery("format", "")
	status := h.gowaSvc.GetStatus()

	// If already authenticated
	if status.IsLoggedIn {
		if format == "json" {
			c.JSON(http.StatusOK, gin.H{
				"status":  "already_logged_in",
				"phone":   status.PhoneNumber,
				"jid":     status.JID,
				"message": "WhatsApp device is already authenticated and active.",
			})
			return
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		h.alreadyLoggedInTemplate.Execute(c.Writer, status)
		return
	}

	pngBytes, err := h.gowaSvc.GetLatestQRPNG(280)
	if err != nil {
		if format == "json" {
			c.JSON(http.StatusNotFound, gin.H{
				"status":  "qr_not_ready",
				"message": "QR code is not generated yet or expired. Please refresh momentarily.",
			})
			return
		}

		c.Header("Content-Type", "text/html; charset=utf-8")
		h.waitingForQRTemplate.Execute(c.Writer, nil)
		return
	}

	if format == "image" || format == "png" {
		c.Data(http.StatusOK, "image/png", pngBytes)
		return
	}

	if format == "json" {
		rawCode, lastGen, _ := h.gowaSvc.GetLatestQR()
		c.JSON(http.StatusOK, gin.H{
			"status":       "qr_available",
			"qr_code":      rawCode,
			"generated_at": lastGen.Format(time.RFC3339),
		})
		return
	}

	base64Image := base64.StdEncoding.EncodeToString(pngBytes)
	c.Header("Content-Type", "text/html; charset=utf-8")
	h.qrScannerTemplate.Execute(c.Writer, base64Image)
}

// QRImage serves the active QR code directly as a PNG image stream.
func (h *Handler) QRImage(c *gin.Context) {
	pngBytes, err := h.gowaSvc.GetLatestQRPNG(300)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error": "QR code not ready or session is already authenticated",
		})
		return
	}

	c.Data(http.StatusOK, "image/png", pngBytes)
}






