package settings

// Config carries only the non-sensitive runtime values needed to build the
// payload. The caller decides which values to pass in.
type Config struct {
	MaxJSONBytes            int64
	MaxUploadBytes          int64
	FormRateLimit           int
	FormRateWindowSeconds   int
	PublicCORSOrigins       []string
	AdminPageSize           int
	PublicPageSize          int
	SubmissionExportMaxRows int
	AcceptedContentTypes    []string
	ImageVariants           []string
}

// Service builds the sanitized settings payload. It has no storage: the
// values change only on server restart.
type Service struct {
	settings Settings
}

// NewService builds the payload once; every read returns the same snapshot.
func NewService(cfg Config) *Service {
	origins := cfg.PublicCORSOrigins
	if origins == nil {
		origins = []string{}
	}
	return &Service{settings: build(cfg)}
}

// Get returns the sanitized settings payload.
func (s *Service) Get() Settings { return s.settings }

func build(cfg Config) Settings {
	origins := cfg.PublicCORSOrigins
	if origins == nil {
		origins = []string{}
	}
	accepted := cfg.AcceptedContentTypes
	if accepted == nil {
		accepted = []string{}
	}
	variants := cfg.ImageVariants
	if variants == nil {
		variants = []string{}
	}
	return Settings{
		Limits: Limits{
			MaxJSONBytes:            cfg.MaxJSONBytes,
			MaxUploadBytes:          cfg.MaxUploadBytes,
			FormRateLimit:           cfg.FormRateLimit,
			FormRateWindowSeconds:   cfg.FormRateWindowSeconds,
			AdminPageSize:           cfg.AdminPageSize,
			PublicPageSize:          cfg.PublicPageSize,
			SubmissionExportMaxRows: cfg.SubmissionExportMaxRows,
		},
		PublicAPI: PublicAPI{
			RuntimePath: "/api/v2/runtime?path={path}",
			FormsPath:   "/api/v2/forms/{key}/submissions",
			AdminPath:   "/api/v1/admin",
			MediaPath:   "/media/{id}",
			CORSEnabled: len(origins) > 0,
			CORSOrigins: origins,
		},
		Media: Media{
			AcceptedTypes:  accepted,
			MaxUploadBytes: cfg.MaxUploadBytes,
			ImageVariants:  variants,
		},
		Roles: roles(),
	}
}
