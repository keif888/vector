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
go run vector.go -db=qdrant -dsn="host=localhost port=6334 api-key=photoprism" -action=loadcsv -filename=./output/test.csv

go run vector.go -db=sqlite -dsn=./output/develop.db -action=query
go run vector.go -db=mysql -dsn="develop:develop@tcp(localhost:4001)/develop?charset=utf8mb4,utf8&collation=utf8mb4_unicode_ci&parseTime=true" -action=query
go run vector.go -db=postgres -dsn="postgresql://develop:develop@localhost:4002/develop?TimeZone=UTC&connect_timeout=15&lock_timeout=5000&sslmode=disable" -action=query
go run vector.go -db=qdrant -dsn="host=localhost port=6334 api-key=photoprism" -action=query

```

## Issues

1. Postgres needs each database that uses vector to enable the extension.  This needs the command ```CREATE EXTENSION IF NOT EXISTS vector``` to be executed with superuser rights.
1. If you use superuser in Postgres to create the initial tables, then the database owner doesn't have access.  (This can be worked around.)
1. Gorm can NOT handle the table definition of the virtual table with embeddings with AutoMigrate.
1. sqlite-vec does not support blob or primary key in a virtual table.
1. Qdrant doesn't return a "distance" via cosine that is like MariaDB, Postgres or SQLite.  Qdrant returns -1 to 1, where 1 is really close, and -1 is a LONG way away.  MariaDB and co all return 0 as close, and other values are further away.  Please note that the Qdrant result can be converted to the MariaDB or Postgres number, by subracting it from 1.0  ie. (1.0 - Qdrant score).
1. MariaDB, Postgres and Qdrant don't calculate the distance between two embeds to the exact same value. It is close, but they are not the same.
1. SQLite's cosine number bears little or no resemblance to MariaDB, Postgres or Qdrant
1. SqLite does note support order by distance desc
1. Qdrant doesn't have a count that allows a distance/score.  Only filters on the payload. (Workaround is to retreive all the matches > than score)
1. Qdrant only supports >= score for Search (score_threshold)



```
Point distance (same data loaded into each DBMS)
MariaDB
b'mtbqjkz00jgjwufb'	0.0
b'mtbxkctd8qb2qmtz'	8.809711848911661e-10
b'mtbxkctf1zt8xjvj'	0.9255060683302209
b'mtbxkct43bgqynpb'	0.9255088619452182
b'mtbxkct4iykk0p53'	0.9381353707516715
b'mtbxkctyngkfrfx5'	0.9387446717823285

Postgres
mtbqjkz00jgjwufb	0.0
mtbxkctd8qb2qmtz	0.0
mtbxkctf1zt8xjvj	0.9255060639273937
mtbxkct43bgqynpb	0.9255088496906709
mtbxkct4iykk0p53	0.9381353599045968
mtbxkctyngkfrfx5	0.93874467330661

SQLite
mtbqjkz00jgjwufb	0.0
mtbxkctd8qb2qmtz	5.80339474254288e-05
mtbxkct43bgqynpb	1.36052250862122
mtbxkctf1zt8xjvj	1.36052370071411
mtbxkct4iykk0p53	1.3697737455368
mtbxkctyngkfrfx5	1.37021815776825

Qdrant
mtbxkctd8qb2qmtz	1.0
mtbqjkz00jgjwufb	1.0
mtbxkctf1zt8xjvj    0.074494
mtbxkct43bgqynpb    0.074491
mtbxkct4iykk0p53    0.061865
mtbxkctyngkfrfx5    0.061255
```
