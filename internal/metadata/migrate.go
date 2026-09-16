package metadata

import "gorm.io/gorm"

// AutoMigrate creates or updates all metadata tables.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&Source{},
		&Load{},
		&Watermark{},
		&LoadRun{},
		&LogEntry{},
	)
}