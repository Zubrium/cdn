module github.com/Zubrium/cdn/engines/router

go 1.26.4

require (
	github.com/Zubrium/cdn/pkg v0.0.0
	github.com/google/uuid v1.6.0
)

replace (
	github.com/Zubrium/cdn/core v0.0.0 => ../../core
	github.com/Zubrium/cdn/engines/content v0.0.0 => ../content
	github.com/Zubrium/cdn/engines/forms v0.0.0 => ../forms
	github.com/Zubrium/cdn/engines/loader v0.0.0 => ../loader
	github.com/Zubrium/cdn/engines/website v0.0.0 => ../website
	github.com/Zubrium/cdn/main v0.0.0 => ../../main
	github.com/Zubrium/cdn/pkg v0.0.0 => ../../pkg
	github.com/Zubrium/cdn/presentation/admin/base v0.0.0 => ../../presentation/admin/base
	github.com/Zubrium/cdn/presentation/admin/content v0.0.0 => ../../presentation/admin/content
	github.com/Zubrium/cdn/presentation/admin/forms v0.0.0 => ../../presentation/admin/forms
	github.com/Zubrium/cdn/presentation/admin/router v0.0.0 => ../../presentation/admin/router
	github.com/Zubrium/cdn/presentation/adminui v0.0.0 => ../../presentation/admin/ui
	github.com/Zubrium/cdn/presentation/fileapi v0.0.0 => ../../presentation/admin/files
	github.com/Zubrium/cdn/presentation/router v0.0.0 => ../../presentation/router
	github.com/Zubrium/cdn/presentation/sitesapi v0.0.0 => ../../presentation/admin/sites
	github.com/Zubrium/cdn/workers/archive v0.0.0 => ../../workers/archive
	github.com/Zubrium/cdn/workers/media v0.0.0 => ../../workers/media
	github.com/Zubrium/cdn/workers/prerender v0.0.0 => ../../workers/prerender
)
