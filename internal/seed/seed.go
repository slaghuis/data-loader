package seed

type SeedFile struct {
	Sources []Source `yaml:"sources"`
	Loads   []Load   `yaml:"loads"`
}

type Source struct {
	Name        string         `yaml:"name"`
	Kind        string         `yaml:"kind"`
	Description string         `yaml:"description"`
	Params      map[string]any `yaml:"params"`
	Enabled     bool           `yaml:"enabled"`
}

type Load struct {
	Source          string `yaml:"source"`
	Name            string `yaml:"name"`
	ObjectName      string `yaml:"object_name"`
	TargetSchema    string `yaml:"target_schema"`
	TargetTable     string `yaml:"target_table"`
	Mode            string `yaml:"mode"`
	WatermarkColumn string `yaml:"watermark_column"`
	WatermarkType   string `yaml:"watermark_type"`
	BatchSize       int    `yaml:"batch_size"`
	AutoCreateTable bool   `yaml:"auto_create_table"`
	CronExpression  string `yaml:"cron_expression"`
	Enabled         bool   `yaml:"enabled"`
}