This repository is a spike to test vector database connectivity and capability for future use within PhotoPrism.  
It's very much not a production ready set of code!  
Use it at your own risk, no guarantee of functionality is provided.  

## Development Commands

```
go run vector.go -filename=./output/test.csv -overwrite
go run vector.go -db=sqlite -dsn=./output/develop.db -action=loadcsv -filename=./output/test.csv
go run vector.go -db=mysql -dsn="develop:develop@tcp(localhost:4001)/develop?charset=utf8mb4,utf8&collation=utf8mb4_unicode_ci&parseTime=true" -action=loadcsv -filename=./output/test.csv
First Run
go run vector.go -db=postgres -dsn="postgresql://photoprism:photoprism@localhost:4002/develop?TimeZone=UTC&connect_timeout=15&lock_timeout=5000&sslmode=disable" -action=loadcsv -filename=./output/test.csv
Subsequent runs
go run vector.go -db=postgres -dsn="postgresql://develop:develop@localhost:4002/develop?TimeZone=UTC&connect_timeout=15&lock_timeout=5000&sslmode=disable" -action=loadcsv -filename=./output/test.csv

go run vector.go -db=sqlite -dsn=./output/develop.db -action=query
go run vector.go -db=mysql -dsn="develop:develop@tcp(localhost:4001)/develop?charset=utf8mb4,utf8&collation=utf8mb4_unicode_ci&parseTime=true" -action=query
go run vector.go -db=postgres -dsn="postgresql://develop:develop@localhost:4002/develop?TimeZone=UTC&connect_timeout=15&lock_timeout=5000&sslmode=disable" -action=query

```

## Issues

1. Postgres needs each database that uses vector to enable the extension.  This needs the command ```CREATE EXTENSION IF NOT EXISTS vector``` to be executed with superuser rights.
1. If you use superuser in Postgres to create the initial tables, then the database owner doesn't have access.
1. Gorm can NOT handle the table definition of the virtual table with embeddings with AutoMigrate.
1. sqlite-vec does not support blob or primary key in a virtual table.

