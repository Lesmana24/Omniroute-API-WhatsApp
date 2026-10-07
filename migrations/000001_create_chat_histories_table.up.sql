-- Migration: 000001_create_chat_histories_table.up.sql
-- Description: Creates the chat_histories table for persisting WhatsApp conversation context with Omniroute AI.

CREATE TABLE IF NOT EXISTS chat_histories (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    phone_number VARCHAR(32) NOT NULL,
    role VARCHAR(16) NOT NULL CHECK (role IN ('user', 'assistant', 'system')),
    content TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Composite index optimized for retrieving recent N messages per phone number in chronological flow
CREATE INDEX IF NOT EXISTS idx_chat_histories_phone_created 
    ON chat_histories (phone_number, created_at DESC, id DESC);

-- Index for time-based retention pruning, analytics, and reporting
CREATE INDEX IF NOT EXISTS idx_chat_histories_created_at 
    ON chat_histories (created_at);
