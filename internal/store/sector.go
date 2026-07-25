package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/hoax/go-stock/internal/provider"
)

func (s *Store) ReplaceSectors(ctx context.Context, sectorType string, sectors []provider.Sector, constituents []provider.SectorConstituent) error {
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, "DELETE sc FROM sector_constituent sc INNER JOIN sector_basic sb ON sb.sector_code=sc.sector_code WHERE sb.sector_type=?", sectorType); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sector_basic WHERE sector_type=?", sectorType); err != nil {
		return err
	}
	for _, sector := range sectors {
		if _, err := tx.ExecContext(ctx, "INSERT INTO sector_basic (sector_code,sector_type,sector_name) VALUES (?,?,?)", sector.Code, sector.Type, sector.Name); err != nil {
			return fmt.Errorf("insert sector %s: %w", sector.Code, err)
		}
	}
	for _, item := range constituents {
		if _, err := tx.ExecContext(ctx, "INSERT IGNORE INTO sector_constituent (sector_code,symbol) VALUES (?,?)", item.SectorCode, item.Symbol); err != nil {
			return fmt.Errorf("insert sector constituent %s/%s: %w", item.SectorCode, item.Symbol, err)
		}
	}
	return tx.Commit()
}

func (s *Store) SectorMembershipExists(ctx context.Context, sectorType string) (bool, error) {
	var count int
	err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sector_basic sb INNER JOIN sector_constituent sc ON sc.sector_code=sb.sector_code WHERE sb.sector_type=?`, sectorType).Scan(&count)
	return count > 0, err
}

func sectorTypeColumn(groupBy string) string {
	if strings.EqualFold(groupBy, "concept") {
		return "concept"
	}
	return "industry"
}
