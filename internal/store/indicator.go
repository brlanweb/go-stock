package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/hoax/go-stock/internal/indicator"
)

func (s *Store) SeedIndicatorCatalog(ctx context.Context) error {
	for _, item := range indicator.Catalog() {
		defaults, _ := json.Marshal(item.DefaultParams)
		currents, _ := json.Marshal(item.CurrentParams)
		_, err := s.DB.ExecContext(ctx, `INSERT INTO indicator_definition
			(indicator_id,display_name,description,category,kind,capability,source,enabled,default_params,current_params,sort_order)
			VALUES (?,?,?,?,?,?,?,?,?,?,?)
			ON DUPLICATE KEY UPDATE display_name=VALUES(display_name),description=VALUES(description),category=VALUES(category),kind=VALUES(kind),capability=VALUES(capability),source=VALUES(source),default_params=VALUES(default_params),sort_order=VALUES(sort_order)`,
			item.ID, item.DisplayName, item.Description, item.Category, item.Kind, item.Capability, item.Source, item.Enabled, string(defaults), string(currents), item.SortOrder)
		if err != nil {
			return fmt.Errorf("初始化指标 %s 失败: %w", item.ID, err)
		}
	}
	return nil
}

func (s *Store) ListIndicators(ctx context.Context) ([]indicator.Definition, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT indicator_id,display_name,description,category,kind,capability,source,enabled,default_params,current_params,sort_order FROM indicator_definition ORDER BY sort_order,indicator_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]indicator.Definition, 0)
	for rows.Next() {
		var item indicator.Definition
		var defaults, currents []byte
		if err := rows.Scan(&item.ID, &item.DisplayName, &item.Description, &item.Category, &item.Kind, &item.Capability, &item.Source, &item.Enabled, &defaults, &currents, &item.SortOrder); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(defaults, &item.DefaultParams); err != nil {
			return nil, fmt.Errorf("指标 %s 默认参数无效: %w", item.ID, err)
		}
		if err := json.Unmarshal(currents, &item.CurrentParams); err != nil {
			return nil, fmt.Errorf("指标 %s 当前参数无效: %w", item.ID, err)
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *Store) GetIndicator(ctx context.Context, id string) (*indicator.Definition, error) {
	var item indicator.Definition
	var defaults, currents []byte
	err := s.DB.QueryRowContext(ctx, `SELECT indicator_id,display_name,description,category,kind,capability,source,enabled,default_params,current_params,sort_order FROM indicator_definition WHERE indicator_id=?`, id).
		Scan(&item.ID, &item.DisplayName, &item.Description, &item.Category, &item.Kind, &item.Capability, &item.Source, &item.Enabled, &defaults, &currents, &item.SortOrder)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(defaults, &item.DefaultParams); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(currents, &item.CurrentParams); err != nil {
		return nil, err
	}
	return &item, nil
}

func (s *Store) UpdateIndicator(ctx context.Context, id string, enabled bool, params map[string]any) error {
	item, err := s.GetIndicator(ctx, id)
	if err != nil {
		return err
	}
	if item == nil {
		return sql.ErrNoRows
	}
	if params == nil {
		params = item.CurrentParams
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return err
	}
	_, err = s.DB.ExecContext(ctx, "UPDATE indicator_definition SET enabled=?,current_params=? WHERE indicator_id=?", enabled, string(raw), id)
	return err
}

func (s *Store) ResetIndicator(ctx context.Context, id string) error {
	result, err := s.DB.ExecContext(ctx, "UPDATE indicator_definition SET current_params=default_params,enabled=1 WHERE indicator_id=?", id)
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}
