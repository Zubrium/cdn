module github.com/Zubrium/cdn/presentation/router

go 1.26.4

require (
	github.com/Zubrium/cdn/engines/content v0.0.0
	github.com/Zubrium/cdn/engines/forms v0.0.0
	github.com/Zubrium/cdn/engines/loader v0.0.0
	github.com/Zubrium/cdn/engines/router v0.0.0
	github.com/Zubrium/cdn/pkg v0.0.0
	github.com/google/uuid v1.6.0
)

require golang.org/x/text v0.43.0 // indirect

replace (
	github.com/Zubrium/cdn/engines/content v0.0.0 => ../../engines/content
	github.com/Zubrium/cdn/engines/forms v0.0.0 => ../../engines/forms
	github.com/Zubrium/cdn/engines/loader v0.0.0 => ../../engines/loader
	github.com/Zubrium/cdn/engines/router v0.0.0 => ../../engines/router
	github.com/Zubrium/cdn/pkg v0.0.0 => ../../pkg
	github.com/Zubrium/cdn/presentation/admin/base v0.0.0 => ../admin/base
)
