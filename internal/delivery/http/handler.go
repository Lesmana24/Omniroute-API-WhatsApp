package http

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"omniroute-api-wa/internal/config"
	"omniroute-api-wa/internal/domain"
	"omniroute-api-wa/internal/repository/postgres"
	"omniroute-api-wa/internal/service"
)

// Handler handles HTTP requests for monitoring and WhatsApp pairing.
type Handler struct {
	cfg       *config.Config
	chatRepo  postgres.ChatRepository
	gowaSvc   service.GoWAService
	startTime time.Time
}

// NewHandler creates a new HTTP handler instance.
func NewHandler(cfg *config.Config, chatRepo postgres.ChatRepository, gowaSvc service.GoWAService) *Handler {
	return &Handler{
		cfg:       cfg,
		chatRepo:  chatRepo,
		gowaSvc:   gowaSvc,
		startTime: time.Now(),
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
		c.String(http.StatusOK, renderAlreadyLoggedInHTML(status))
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
		c.String(http.StatusOK, renderWaitingForQRHTML())
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
	c.String(http.StatusOK, renderQRScannerHTML(base64Image))
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

func renderAlreadyLoggedInHTML(status domain.WhatsAppStatus) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="id">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>WhatsApp Terhubung - Omniroute AI</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            background: #0f172a;
            color: #f8fafc;
            display: flex;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
            margin: 0;
            padding: 20px;
        }
        .card {
            background: #1e293b;
            border: 1px solid #334155;
            border-radius: 16px;
            padding: 36px 32px;
            max-width: 440px;
            width: 100%%;
            text-align: center;
            box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.4);
        }
        .badge-success {
            display: inline-block;
            background: rgba(34, 197, 94, 0.2);
            color: #4ade80;
            border: 1px solid #22c55e;
            padding: 6px 14px;
            border-radius: 9999px;
            font-weight: 600;
            font-size: 13px;
            margin-bottom: 20px;
        }
        h1 { font-size: 24px; margin: 0 0 10px; color: #f8fafc; }
        p { color: #94a3b8; font-size: 14px; line-height: 1.6; margin: 0 0 24px; }
        .info-box {
            background: #0f172a;
            border: 1px solid #334155;
            border-radius: 10px;
            padding: 16px;
            text-align: left;
            margin-bottom: 24px;
            font-size: 13px;
        }
        .info-row { display: flex; justify-content: space-between; margin-bottom: 8px; }
        .info-row:last-child { margin-bottom: 0; }
        .info-label { color: #64748b; }
        .info-value { color: #f1f5f9; font-family: monospace; font-weight: bold; }
        .btn {
            display: inline-block;
            background: #2563eb;
            color: #ffffff;
            text-decoration: none;
            padding: 10px 20px;
            border-radius: 8px;
            font-weight: 500;
            font-size: 14px;
            transition: background 0.2s;
        }
        .btn:hover { background: #1d4ed8; }
    </style>
</head>
<body>
    <div class="card">
        <div class="badge-success">&#10003; WhatsApp Terhubung</div>
        <h1>Perangkat Aktif</h1>
        <p>Sesi WhatsApp Anda telah terotentikasi dan siap memproses pesan secara otomatis dengan Omniroute AI.</p>
        <div class="info-box">
            <div class="info-row">
                <span class="info-label">Nomor WhatsApp:</span>
                <span class="info-value">+%s</span>
            </div>
            <div class="info-row">
                <span class="info-label">Nama Push:</span>
                <span class="info-value">%s</span>
            </div>
            <div class="info-row">
                <span class="info-label">JID Akun:</span>
                <span class="info-value">%s</span>
            </div>
        </div>
        <a href="/status" class="btn">Lihat Status API</a>
    </div>
</body>
</html>`, status.PhoneNumber, status.PushName, status.JID)
}

func renderWaitingForQRHTML() string {
	return `<!DOCTYPE html>
<html lang="id">
<head>
    <meta charset="UTF-8">
    <meta http-equiv="refresh" content="3">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Menyiapkan QR Code - Omniroute AI</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            background: #0f172a;
            color: #f8fafc;
            display: flex;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
            margin: 0;
            padding: 20px;
        }
        .card {
            background: #1e293b;
            border: 1px solid #334155;
            border-radius: 16px;
            padding: 36px 32px;
            max-width: 440px;
            width: 100%;
            text-align: center;
            box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.4);
        }
        .spinner {
            border: 4px solid rgba(255, 255, 255, 0.1);
            border-left-color: #38bdf8;
            border-radius: 50%;
            width: 44px;
            height: 44px;
            animation: spin 1s linear infinite;
            margin: 0 auto 20px;
        }
        @keyframes spin { 0% { transform: rotate(0deg); } 100% { transform: rotate(360deg); } }
        h1 { font-size: 22px; margin: 0 0 10px; color: #f8fafc; }
        p { color: #94a3b8; font-size: 14px; margin: 0; }
    </style>
</head>
<body>
    <div class="card">
        <div class="spinner"></div>
        <h1>Menghubungkan ke WhatsApp...</h1>
        <p>Sedang membuat kode QR sesi. Halaman ini akan memuat ulang secara otomatis dalam beberapa detik.</p>
    </div>
</body>
</html>`
}

func renderQRScannerHTML(base64Image string) string {
	return fmt.Sprintf(`<!DOCTYPE html>
<html lang="id">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Tautkan WhatsApp - Omniroute AI</title>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, Helvetica, Arial, sans-serif;
            background: #0f172a;
            color: #f8fafc;
            display: flex;
            align-items: center;
            justify-content: center;
            min-height: 100vh;
            margin: 0;
            padding: 20px;
        }
        .card {
            background: #1e293b;
            border: 1px solid #334155;
            border-radius: 16px;
            padding: 32px;
            max-width: 420px;
            width: 100%%;
            text-align: center;
            box-shadow: 0 20px 25px -5px rgba(0, 0, 0, 0.4);
        }
        .badge {
            display: inline-block;
            background: rgba(56, 189, 248, 0.15);
            color: #38bdf8;
            border: 1px solid rgba(56, 189, 248, 0.4);
            padding: 5px 12px;
            border-radius: 9999px;
            font-size: 12px;
            font-weight: 600;
            margin-bottom: 16px;
        }
        h1 { font-size: 22px; margin: 0 0 8px; color: #f8fafc; }
        p { color: #94a3b8; font-size: 13px; line-height: 1.5; margin: 0 0 20px; }
        .qr-wrapper {
            background: #ffffff;
            padding: 16px;
            border-radius: 12px;
            display: inline-block;
            margin-bottom: 24px;
            box-shadow: 0 4px 6px -1px rgba(0, 0, 0, 0.2);
        }
        .qr-wrapper img {
            display: block;
            width: 250px;
            height: 250px;
        }
        .instructions {
            text-align: left;
            background: #0f172a;
            border: 1px solid #334155;
            border-radius: 10px;
            padding: 16px;
            font-size: 13px;
            color: #cbd5e1;
            margin-bottom: 18px;
        }
        .instructions ol {
            margin: 0;
            padding-left: 20px;
        }
        .instructions li {
            margin-bottom: 6px;
        }
        .instructions li:last-child {
            margin-bottom: 0;
        }
        .countdown {
            font-size: 12px;
            color: #64748b;
        }
    </style>
</head>
<body>
    <div class="card">
        <div class="badge">Autentikasi WhatsApp</div>
        <h1>Pindai Kode QR</h1>
        <p>Buka WhatsApp pada smartphone Anda untuk menautkan perangkat ini.</p>
        
        <div class="qr-wrapper">
            <img src="data:image/png;base64,%s" alt="WhatsApp QR Code" />
        </div>

        <div class="instructions">
            <ol>
                <li>Buka aplikasi WhatsApp di HP Anda</li>
                <li>Buka <strong>Pengaturan / Titik Tiga</strong> &gt; <strong>Perangkat Tertaut</strong></li>
                <li>Ketuk <strong>Tautkan Perangkat</strong></li>
                <li>Arahkan kamera ke kode QR di atas</li>
            </ol>
        </div>

        <div class="countdown" id="timer">Memeriksa status secara berkala...</div>
    </div>

    <script>
        // Check pairing status every 3 seconds
        setInterval(async () => {
            try {
                const res = await fetch('/status');
                const data = await res.json();
                if (data.whatsapp && data.whatsapp.is_logged_in) {
                    window.location.reload();
                }
            } catch (e) {
                // Ignore transient network errors
            }
        }, 3000);

        // Auto reload page every 25 seconds to grab refreshed QR if unlinked
        setTimeout(() => {
            window.location.reload();
        }, 25000);
    </script>
</body>
</html>`, base64Image)
}
