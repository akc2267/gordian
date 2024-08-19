# protoc --go_out=. --go_opt=paths=source_relative --go-grpc_out=. --go-grpc_opt=paths=source_relative proto/**/*.proto
protoc --go_out=. --go-grpc_out=. proto/**/*.proto

cp ./github.com/rollchains/gordian/gcosmos/gserver/internal/grpc/* ./gserver/internal/grpc
rm -rf ./github.com


# TODO: generate rust grpc client