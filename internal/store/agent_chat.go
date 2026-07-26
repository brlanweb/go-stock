package store

import (
	"context"
	"strings"
	"time"
)

const maxAgentChatMessages = 200

type AgentChatMessage struct {
	ID        uint64    `json:"id"`
	Symbol    string    `json:"symbol"`
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	CreatedAt time.Time `json:"created_at"`
}

func (s *Store) AgentChatMessages(ctx context.Context, symbol string) ([]AgentChatMessage, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id,symbol,role,content,created_at
		FROM (
			SELECT id,symbol,role,content,created_at
			FROM agent_chat_message WHERE symbol=? ORDER BY id DESC LIMIT ?
		) recent ORDER BY id`, symbol, maxAgentChatMessages)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	messages := make([]AgentChatMessage, 0)
	for rows.Next() {
		var message AgentChatMessage
		if err := rows.Scan(&message.ID, &message.Symbol, &message.Role, &message.Content, &message.CreatedAt); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}
	return messages, rows.Err()
}

func (s *Store) AppendAgentChatMessage(ctx context.Context, symbol, role, content string) error {
	content = strings.TrimSpace(content)
	if content == "" || (role != "user" && role != "assistant") {
		return nil
	}
	_, err := s.DB.ExecContext(ctx,
		"INSERT INTO agent_chat_message (symbol,role,content) VALUES (?,?,?)",
		symbol, role, content)
	return err
}

func (s *Store) ClearAgentChatMessages(ctx context.Context, symbol string) error {
	_, err := s.DB.ExecContext(ctx, "DELETE FROM agent_chat_message WHERE symbol=?", symbol)
	return err
}
