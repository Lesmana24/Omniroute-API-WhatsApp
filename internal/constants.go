package internal

import (
	"time"
)

// Default configuration values
const (
	DefaultMaxContextMessages = 10
	DefaultWorkerPoolSize     = 5
	DefaultWorkerQueueSize    = 100
)

// HTTP timeouts
const (
	HTTPReadWriteTimeout = 15 * time.Second
	HTTPIdleTimeout      = 60 * time.Second
	ShutdownTimeout      = 30 * time.Second
)

// Database connection settings
const (
	DBConnectTimeout     = 10 * time.Second
	DBHealthCheckPeriod  = 1 * time.Minute
	DBMaxConnLifetime    = 1 * time.Hour
	DBMaxConnIdleTime    = 15 * time.Minute
)

// AI service timeouts
const (
	AIIOTimeout          = 60 * time.Second
	WorkerJobTimeout     = 90 * time.Second
)

// WhatsApp message limits
const (
	MaxWhatsAppChunkSize = 3800
)