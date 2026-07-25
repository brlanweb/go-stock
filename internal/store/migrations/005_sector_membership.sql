CREATE TABLE IF NOT EXISTS sector_basic (
    sector_code VARCHAR(16) NOT NULL,
    sector_type ENUM('industry','concept') NOT NULL,
    sector_name VARCHAR(64) NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (sector_code),
    KEY idx_sector_type_name (sector_type, sector_name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='行业与概念板块';

CREATE TABLE IF NOT EXISTS sector_constituent (
    sector_code VARCHAR(16) NOT NULL,
    symbol VARCHAR(24) NOT NULL,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    PRIMARY KEY (sector_code, symbol),
    KEY idx_sector_symbol (symbol, sector_code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_general_ci COMMENT='板块成分股';
