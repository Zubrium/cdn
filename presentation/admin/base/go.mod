module github.com/Zubrium/cdn/presentation/admin/base

go 1.26.4

require (
	github.com/Zubrium/cdn/engines/content v0.0.0
	github.com/Zubrium/cdn/engines/forms v0.0.0
	github.com/Zubrium/cdn/engines/router v0.0.0
	github.com/Zubrium/cdn/engines/website v0.0.0
	github.com/Zubrium/cdn/pkg v0.0.0
	github.com/Zubrium/cdn/presentation/admin/content v0.0.0
	github.com/Zubrium/cdn/presentation/admin/forms v0.0.0
	github.com/Zubrium/cdn/presentation/admin/router v0.0.0
	github.com/Zubrium/cdn/presentation/fileapi v0.0.0
	github.com/Zubrium/cdn/presentation/httpauth v0.0.0
	github.com/Zubrium/cdn/presentation/router v0.0.0
	github.com/Zubrium/cdn/presentation/sitesapi v0.0.0
	github.com/google/uuid v1.6.0
)

require (
	github.com/Zubrium/cdn/engines/loader v0.0.0 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.30.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp v0.23.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.47.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.47.0 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/sdk v1.47.0 // indirect
	go.opentelemetry.io/otel/sdk/log v1.47.0 // indirect
	go.opentelemetry.io/otel/sdk/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	go.opentelemetry.io/proto/otlp v1.11.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260928230214-8a89bd6388cc // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc // indirect
	google.golang.org/grpc v1.83.2 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace (
	github.com/Zubrium/cdn/core v0.0.0 => ../../../core
	github.com/Zubrium/cdn/engines/content v0.0.0 => ../../../engines/content
	github.com/Zubrium/cdn/engines/forms v0.0.0 => ../../../engines/forms
	github.com/Zubrium/cdn/engines/loader v0.0.0 => ../../../engines/loader
	github.com/Zubrium/cdn/engines/router v0.0.0 => ../../../engines/router
	github.com/Zubrium/cdn/engines/website v0.0.0 => ../../../engines/website
	github.com/Zubrium/cdn/main v0.0.0 => ../../../main
	github.com/Zubrium/cdn/pkg v0.0.0 => ../../../pkg
	github.com/Zubrium/cdn/presentation/admin/content v0.0.0 => ../content
	github.com/Zubrium/cdn/presentation/admin/forms v0.0.0 => ../forms
	github.com/Zubrium/cdn/presentation/admin/router v0.0.0 => ../router
	github.com/Zubrium/cdn/presentation/adminui v0.0.0 => ../ui
	github.com/Zubrium/cdn/presentation/fileapi v0.0.0 => ../files
	github.com/Zubrium/cdn/presentation/httpauth v0.0.0 => ../../httpauth
	github.com/Zubrium/cdn/presentation/router v0.0.0 => ../../router
	github.com/Zubrium/cdn/presentation/sitesapi v0.0.0 => ../sites
	github.com/Zubrium/cdn/workers/archive v0.0.0 => ../../../workers/archive
	github.com/Zubrium/cdn/workers/media v0.0.0 => ../../../workers/media
	github.com/Zubrium/cdn/workers/prerender v0.0.0 => ../../../workers/prerender
)
