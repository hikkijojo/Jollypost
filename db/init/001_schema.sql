CREATE TABLE IF NOT EXISTS categories (
  id BIGINT NOT NULL AUTO_INCREMENT,
  name VARCHAR(128) NOT NULL,
  sort_order INT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_categories_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS category_lines (
  id BIGINT NOT NULL AUTO_INCREMENT,
  category_id BIGINT NOT NULL,
  body VARCHAR(300) NOT NULL,
  sort_order INT NOT NULL DEFAULT 0,
  PRIMARY KEY (id),
  KEY idx_category_lines_category (category_id),
  CONSTRAINT fk_category_lines_category FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS products (
  id BIGINT NOT NULL AUTO_INCREMENT,
  category_id BIGINT NULL,
  name VARCHAR(128) NOT NULL,
  enabled TINYINT(1) NOT NULL DEFAULT 1,
  sort_order INT NOT NULL DEFAULT 0,
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  PRIMARY KEY (id),
  KEY idx_products_category (category_id),
  CONSTRAINT fk_products_category FOREIGN KEY (category_id) REFERENCES categories (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS daily_seq (
  day DATE NOT NULL,
  last_no INT NOT NULL,
  PRIMARY KEY (day)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS tasks (
  id BIGINT NOT NULL AUTO_INCREMENT,
  code VARCHAR(16) NOT NULL,
  code_year SMALLINT NOT NULL,
  threads_url VARCHAR(512) NOT NULL,
  product_text VARCHAR(128) NOT NULL DEFAULT '',
  product_id BIGINT NULL,
  reply_text VARCHAR(500) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  created_by VARCHAR(64) NOT NULL,
  created_by_name VARCHAR(128) NOT NULL DEFAULT '',
  created_at DATETIME NOT NULL,
  updated_at DATETIME NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uq_tasks_code (code_year, code),
  KEY idx_tasks_created (created_at),
  KEY idx_tasks_url (threads_url),
  CONSTRAINT fk_tasks_product FOREIGN KEY (product_id) REFERENCES products (id) ON DELETE SET NULL
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS task_replies (
  task_id BIGINT NOT NULL,
  user_id VARCHAR(64) NOT NULL,
  user_name VARCHAR(128) NOT NULL DEFAULT '',
  reply_text VARCHAR(500) NOT NULL DEFAULT '',
  status VARCHAR(16) NOT NULL DEFAULT 'pending',
  updated_at DATETIME NOT NULL,
  PRIMARY KEY (task_id, user_id),
  CONSTRAINT fk_task_replies_task FOREIGN KEY (task_id) REFERENCES tasks (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS proofs (
  task_id BIGINT NOT NULL,
  user_id VARCHAR(64) NOT NULL,
  created_at DATETIME NOT NULL,
  PRIMARY KEY (task_id, user_id),
  CONSTRAINT fk_proofs_task FOREIGN KEY (task_id) REFERENCES tasks (id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS thread_previews (
  url_hash CHAR(64) NOT NULL,
  author VARCHAR(160) NOT NULL DEFAULT '',
  body VARCHAR(500) NOT NULL DEFAULT '',
  has_image TINYINT(1) NOT NULL DEFAULT 0,
  fetched_at DATETIME NOT NULL,
  PRIMARY KEY (url_hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS app_meta (
  k VARCHAR(32) NOT NULL,
  v VARCHAR(255) NOT NULL,
  PRIMARY KEY (k)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
