module github.com/Zubrium/cdn/main

go 1.26.4

require (
	github.com/Zubrium/cdn/core v0.0.0
	github.com/Zubrium/cdn/engines/content v0.0.0
	github.com/Zubrium/cdn/engines/forms v0.0.0
	github.com/Zubrium/cdn/engines/loader v0.0.0
	github.com/Zubrium/cdn/engines/router v0.0.0
	github.com/Zubrium/cdn/engines/website v0.0.0
	github.com/Zubrium/cdn/pkg v0.0.0
	github.com/Zubrium/cdn/presentation/admin/base v0.0.0
	github.com/Zubrium/cdn/presentation/admin/content v0.0.0
	github.com/Zubrium/cdn/presentation/admin/forms v0.0.0
	github.com/Zubrium/cdn/presentation/admin/notification v0.0.0
	github.com/Zubrium/cdn/presentation/admin/router v0.0.0
	github.com/Zubrium/cdn/presentation/adminui v0.0.0
	github.com/Zubrium/cdn/presentation/fileapi v0.0.0
	github.com/Zubrium/cdn/presentation/router v0.0.0
	github.com/Zubrium/cdn/presentation/sitesapi v0.0.0
	github.com/Zubrium/cdn/workers/archive v0.0.0
	github.com/Zubrium/cdn/workers/media v0.0.0
	github.com/Zubrium/cdn/workers/prerender v0.0.0
	github.com/google/uuid v1.6.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	ariga.io/atlas v1.3.0 // indirect
	github.com/Zubrium/cdn/presentation/admin/auth v0.0.0 // indirect
	github.com/aws/aws-sdk-go-v2 v1.36.3 // indirect
	github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream v1.6.10 // indirect
	github.com/aws/aws-sdk-go-v2/config v1.29.9 // indirect
	github.com/aws/aws-sdk-go-v2/credentials v1.17.62 // indirect
	github.com/aws/aws-sdk-go-v2/feature/ec2/imds v1.16.30 // indirect
	github.com/aws/aws-sdk-go-v2/internal/configsources v1.3.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/endpoints/v2 v2.6.34 // indirect
	github.com/aws/aws-sdk-go-v2/internal/ini v1.8.3 // indirect
	github.com/aws/aws-sdk-go-v2/internal/v4a v1.3.34 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/accept-encoding v1.12.3 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/checksum v1.6.2 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/presigned-url v1.12.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/internal/s3shared v1.18.15 // indirect
	github.com/aws/aws-sdk-go-v2/service/s3 v1.78.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/sso v1.25.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/ssooidc v1.29.1 // indirect
	github.com/aws/aws-sdk-go-v2/service/sts v1.33.17 // indirect
	github.com/aws/smithy-go v1.22.2 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.30.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.7.2 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
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
	golang.org/x/crypto v0.58.0 // indirect
	golang.org/x/net v0.60.0 // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/sys v0.49.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260928230214-8a89bd6388cc // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260928230214-8a89bd6388cc // indirect
	google.golang.org/grpc v1.83.2 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)

replace (
	github.com/Zubrium/cdn/core v0.0.0 => ../core
	github.com/Zubrium/cdn/engines/content v0.0.0 => ../engines/content
	github.com/Zubrium/cdn/engines/forms v0.0.0 => ../engines/forms
	github.com/Zubrium/cdn/engines/loader v0.0.0 => ../engines/loader
	github.com/Zubrium/cdn/engines/router v0.0.0 => ../engines/router
	github.com/Zubrium/cdn/engines/website v0.0.0 => ../engines/website
	github.com/Zubrium/cdn/pkg v0.0.0 => ../pkg
	github.com/Zubrium/cdn/presentation/admin/auth v0.0.0 => ../presentation/admin/auth
	github.com/Zubrium/cdn/presentation/admin/base v0.0.0 => ../presentation/admin/base
	github.com/Zubrium/cdn/presentation/admin/content v0.0.0 => ../presentation/admin/content
	github.com/Zubrium/cdn/presentation/admin/forms v0.0.0 => ../presentation/admin/forms
	github.com/Zubrium/cdn/presentation/admin/notification v0.0.0 => ../presentation/admin/notification
	github.com/Zubrium/cdn/presentation/admin/router v0.0.0 => ../presentation/admin/router
	github.com/Zubrium/cdn/presentation/adminui v0.0.0 => ../presentation/admin/ui
	github.com/Zubrium/cdn/presentation/fileapi v0.0.0 => ../presentation/admin/files
	github.com/Zubrium/cdn/presentation/router v0.0.0 => ../presentation/router
	github.com/Zubrium/cdn/presentation/sitesapi v0.0.0 => ../presentation/admin/sites
	github.com/Zubrium/cdn/workers/archive v0.0.0 => ../workers/archive
	github.com/Zubrium/cdn/workers/media v0.0.0 => ../workers/media
	github.com/Zubrium/cdn/workers/prerender v0.0.0 => ../workers/prerender
)
