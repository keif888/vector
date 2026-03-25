package markers

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/keif888/vector/dbms"
	"github.com/keif888/vector/pkg/dsn"
	"github.com/qdrant/go-client/qdrant"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// LoadMarkers retreives the saved markers from fileName and loads them into the table
func LoadMarkers(fileName string, dsn dsn.DSN, equation, batchsize int, log *logrus.Logger) (err error) {
	var db *dbms.DbConn
	start := time.Now()
	switch dsn.Driver {
	case dbms.MySQL:
		if db, err = setupGormDB(dsn.Driver, dsn.ToString()); err != nil {
			db.Close()
			log.Errorf("LoadMarkers: database setup failed with %s", err)
			return err
		}
		defer db.Close()
		if err = migrateVectorMarkers(); err != nil {
			log.Errorf("LoadMarkers: migration of vector_markers failed with %s", err)
			return err
		}
		if DistanceEquation(equation) == Distance_Cosine {
			if err = dbms.Db().Exec("ALTER TABLE `vector_marker_faces` ADD VECTOR INDEX (embedding) M=8 DISTANCE=cosine").Error; err != nil {
				log.Errorf("LoadMarkers: vector_marker_faces index setup failed with %s", err)
				return err
			}
		} else if DistanceEquation(equation) == Distance_Euclidean {
			if err = dbms.Db().Exec("ALTER TABLE `vector_marker_faces` ADD VECTOR INDEX (embedding) M=8 DISTANCE=euclidean").Error; err != nil {
				log.Errorf("LoadMarkers: vector_marker_faces index setup failed with %s", err)
				return err
			}
		} else {
			return fmt.Errorf("equation %d was not valid", equation)
		}
		// What does this do to the tables?
		if err = migrateVectorMarkers(); err != nil {
			log.Errorf("LoadMarkers: migration of vector_markers failed with %s", err)
			return err
		}

	case dbms.Postgres:
		if db, err = setupGormDB(dsn.Driver, dsn.ToString()); err != nil {
			db.Close()
			log.Errorf("LoadMarkers: database setup failed with %s", err)
			return err
		}
		defer db.Close()
		result := gorm.WithResult()
		err = gorm.G[any](dbms.Db(), result).Exec(context.Background(), "select typname from pg_type where typname = ?", "vector")
		if err != nil {
			log.Errorf("LoadMarkers: extension check for vector failed with %s", err)
			return err
		}
		if result.RowsAffected == 0 {
			err = gorm.G[any](dbms.Db(), result).Exec(context.Background(), "CREATE EXTENSION IF NOT EXISTS vector")
			if err != nil {
				log.Errorf("LoadMarkers: extension creation for vector failed with %s", err)
				return err
			}
			type PostgresDBOwners struct {
				DbOwner string
			}
			owner, err := gorm.G[PostgresDBOwners](dbms.Db()).
				Raw("select pg_catalog.pg_get_userbyid(datdba) AS db_owner from pg_catalog.pg_database where datname = current_database()").
				Find(context.Background())
			if err != nil {
				log.Errorf("LoadMarkers: database owner query failed with %s", err)
				return err
			}
			if len(owner) == 1 {
				err = gorm.G[any](dbms.Db(), result).Exec(context.Background(), fmt.Sprintf("ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT ALL ON TABLES TO %s", owner[0].DbOwner))
				if err != nil {
					log.Errorf("LoadMarkers: permission grant to owner failed with %s", err)
					return err
				}
			} else {
				log.Error("LoadMarkers: no database owner returned")
				return fmt.Errorf("%s", "LoadMarkers: no database owner returned")
			}

		}
		if err = migrateVectorMarkers(); err != nil {
			log.Errorf("LoadMarkers: migration of vector_markers failed with %s", err)
			return err
		}

		if DistanceEquation(equation) == Distance_Cosine {
			if err = dbms.Db().Exec("CREATE INDEX ON vector_marker_faces USING hnsw (embedding vector_cosine_ops) WITH (m=24, ef_construction=320)").Error; err != nil {
				log.Errorf("LoadMarkers: vector_marker_faces index setup failed with %s", err)
				return err
			}
		} else if DistanceEquation(equation) == Distance_Euclidean {
			if err = dbms.Db().Exec("CREATE INDEX ON vector_marker_faces USING hnsw (embedding vector_l2_ops) WITH (m=24, ef_construction=320)").Error; err != nil {
				log.Errorf("LoadMarkers: vector_marker_faces index setup failed with %s", err)
				return err
			}
		} else {
			return fmt.Errorf("equation %d was not valid", equation)
		}

		// Does this drop the index?
		if err = migrateVectorMarkers(); err != nil {
			log.Errorf("LoadMarkers: migration of vector_markers failed with %s", err)
			return err
		}

	case dbms.SQLite3:
		if db, err = setupGormDB(dsn.Driver, dsn.ToString()); err != nil {
			db.Close()
			log.Errorf("LoadMarkers: database setup failed with %s", err)
			return err
		}
		defer db.Close()
		// Disable journal to speed up.
		dbms.Db().Exec("PRAGMA journal_mode=OFF")
		// AutoMigrate can not handle an existing virtual table.
		if dbms.Db().Migrator().HasTable("vector_marker_faces") {
			if err = dbms.Db().Migrator().DropTable("vector_marker_faces"); err != nil {
				log.Errorf("LoadMarkers: vector_marker_faces drop failed with %s", err)
				return err
			}
		}
		// No support for blob or primary key!
		if err = dbms.Db().Exec("CREATE VIRTUAL TABLE `vector_marker_faces` USING vec0 (embedding float[512], marker_uid text, embedding_id int)").Error; err != nil {
			log.Errorf("LoadMarkers: vector_marker_faces setup failed with %s", err)
			return err
		}
		if err = migrateVectorMarkers(); err != nil {
			log.Errorf("LoadMarkers: migration of vector_markers failed with %s", err)
			return err
		}
	case dbms.Qdrant:
		if err = setupQdrantDB(dsn); err != nil {
			if err = dbms.QClient().Close(); err != nil {
				log.Errorf("LoadMarkers: Client Close failed with %s", err)
			}
			log.Errorf("LoadMarkers: database setup failed with %s", err)
			return err
		}
		defer func() {
			if err = dbms.QClient().Close(); err != nil {
				log.Errorf("LoadMarkers: Client Close failed with %s", err)
			}
		}()

		var distance qdrant.Distance
		if DistanceEquation(equation) == Distance_Cosine {
			distance = qdrant.Distance_Cosine
		} else if DistanceEquation(equation) == Distance_Euclidean {
			distance = qdrant.Distance_Euclid
		} else {
			return fmt.Errorf("equation %d was not valid", equation)
		}

		if err = dbms.QClient().CreateCollection(context.Background(), &qdrant.CreateCollection{
			CollectionName: VectorMarker{}.TableName(),
			HnswConfig: &qdrant.HnswConfigDiff{
				M:                 qdrant.PtrOf(uint64(24)),    // 16 default (in .yaml file)
				EfConstruct:       qdrant.PtrOf(uint64(320)),   // 100 default (in .yaml file).  200 is balanced build in Qdrant essentials, 320 = 16*20
				FullScanThreshold: qdrant.PtrOf(uint64(10000)), // 0 means that it will always use the index.  10,000 default (in .yaml file)
			},
			VectorsConfig: qdrant.NewVectorsConfig(&qdrant.VectorParams{
				Size: 512,
				// Distance: qdrant.Distance_Cosine,
				// Datatype: qdrant.Datatype_Float32.Enum(),
				Distance: distance,
				Datatype: qdrant.Datatype_Default.Enum(),
				// MultivectorConfig: &qdrant.MultiVectorConfig{
				// 	Comparator: qdrant.MultiVectorComparator_MaxSim,
				// },
			}),
		}); err != nil {
			log.Errorf("LoadMarkers: CreateCollection failed with %s", err)
			return err
		}
		if _, err = dbms.QClient().CreateFieldIndex(context.Background(), &qdrant.CreateFieldIndexCollection{
			CollectionName: VectorMarker{}.TableName(),
			FieldName:      "UID",
			FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
		}); err != nil {
			log.Errorf("LoadMarkers: CreateFieldIndex UID failed with %s", err)
			return err
		}
		if _, err = dbms.QClient().CreateFieldIndex(context.Background(), &qdrant.CreateFieldIndexCollection{
			CollectionName: VectorMarker{}.TableName(),
			FieldName:      "FileUID",
			FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
		}); err != nil {
			log.Errorf("LoadMarkers: CreateFieldIndex FileUID failed with %s", err)
			return err
		}
		if _, err = dbms.QClient().CreateFieldIndex(context.Background(), &qdrant.CreateFieldIndexCollection{
			CollectionName: VectorMarker{}.TableName(),
			FieldName:      "SubjUID",
			FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
		}); err != nil {
			log.Errorf("LoadMarkers: CreateFieldIndex SubjUID failed with %s", err)
			return err
		}
		if _, err = dbms.QClient().CreateFieldIndex(context.Background(), &qdrant.CreateFieldIndexCollection{
			CollectionName: VectorMarker{}.TableName(),
			FieldName:      "SubjSrc",
			FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
		}); err != nil {
			log.Errorf("LoadMarkers: CreateFieldIndex SubjSrc failed with %s", err)
			return err
		}
		if _, err = dbms.QClient().CreateFieldIndex(context.Background(), &qdrant.CreateFieldIndexCollection{
			CollectionName: VectorMarker{}.TableName(),
			FieldName:      "FaceID",
			FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
		}); err != nil {
			log.Errorf("LoadMarkers: CreateFieldIndex FaceID failed with %s", err)
			return err
		}
		if _, err = dbms.QClient().CreateFieldIndex(context.Background(), &qdrant.CreateFieldIndexCollection{
			CollectionName: VectorMarker{}.TableName(),
			FieldName:      "MatchedAt",
			FieldType:      qdrant.FieldType_FieldTypeDatetime.Enum(),
		}); err != nil {
			log.Errorf("LoadMarkers: CreateFieldIndex MatchedAt failed with %s", err)
			return err
		}
		if _, err = dbms.QClient().CreateFieldIndex(context.Background(), &qdrant.CreateFieldIndexCollection{
			CollectionName: VectorMarker{}.TableName(),
			FieldName:      "Clustered",
			FieldType:      qdrant.FieldType_FieldTypeBool.Enum(),
		}); err != nil {
			log.Errorf("LoadMarkers: CreateFieldIndex Clustered failed with %s", err)
			return err
		}

	}

	var csvFile *os.File
	if csvFile, err = os.Open(fileName); err != nil {
		log.Errorf("LoadMarkers: unable to open required file %s with error %s", fileName, err)
		return err
	}
	defer func() {
		if err := csvFile.Close(); err != nil {
			log.Errorf("LoadMarkers: unable to close file %s with error %s", fileName, err)
		}
	}()

	csvReader := csv.NewReader(csvFile)
	markers := make([]VectorMarker, batchsize)
	faceEmbeddings := make([]VectorMarkerFace, batchsize)
	counter := 0
	record := 1
	upto := 0
	headerRead := false
	var csvRecord []string
	var markerReview, markerInvalid bool
	var faceDist float64
	var x, y, w, h float32
	var q, size, score int
	var createdAt, updatedAt time.Time
	var clustered bool
ProcessFileLoop:
	for {
		csvRecord, err = csvReader.Read()
		switch err {
		case io.EOF:
			break ProcessFileLoop
		case nil:
			// NOP
		default:
			log.Errorf("LoadMarkers: unable to read record %d from %s with error %s", record, fileName, err)
			return err
		}
		if !headerRead {
			headerRead = true
			continue
		}
		if markerReview, err = strconv.ParseBool(csvRecord[5]); err != nil {
			log.Errorf("LoadMarkers: unable to to understand bool value from record %d value %s from %s with error %s", record, csvRecord[5], fileName, err)
			return err
		}
		if markerInvalid, err = strconv.ParseBool(csvRecord[6]); err != nil {
			log.Errorf("LoadMarkers: unable to to understand bool value from record %d value %s from %s with error %s", record, csvRecord[6], fileName, err)
			return err
		}
		if faceDist, err = strconv.ParseFloat(csvRecord[10], 64); err != nil {
			log.Errorf("LoadMarkers: unable to to understand float64 value from record %d value %s from %s with error %s", record, csvRecord[10], fileName, err)
			return err
		}
		if x64, err := strconv.ParseFloat(csvRecord[12], 32); err != nil {
			log.Errorf("LoadMarkers: unable to to understand float32 value from record %d value %s from %s with error %s", record, csvRecord[12], fileName, err)
			return err
		} else {
			x = float32(x64)
		}
		if y64, err := strconv.ParseFloat(csvRecord[13], 32); err != nil {
			log.Errorf("LoadMarkers: unable to to understand float32 value from record %d value %s from %s with error %s", record, csvRecord[13], fileName, err)
			return err
		} else {
			y = float32(y64)
		}
		if w64, err := strconv.ParseFloat(csvRecord[14], 32); err != nil {
			log.Errorf("LoadMarkers: unable to to understand float32 value from record %d value %s from %s with error %s", record, csvRecord[14], fileName, err)
			return err
		} else {
			w = float32(w64)
		}
		if h64, err := strconv.ParseFloat(csvRecord[15], 32); err != nil {
			log.Errorf("LoadMarkers: unable to to understand float32 value from record %d value %s from %s with error %s", record, csvRecord[15], fileName, err)
			return err
		} else {
			h = float32(h64)
		}
		if q, err = strconv.Atoi(csvRecord[16]); err != nil {
			log.Errorf("LoadMarkers: unable to to understand int value from record %d value %s from %s with error %s", record, csvRecord[16], fileName, err)
			return err
		}
		if size, err = strconv.Atoi(csvRecord[17]); err != nil {
			log.Errorf("LoadMarkers: unable to to understand int value from record %d value %s from %s with error %s", record, csvRecord[17], fileName, err)
			return err
		}
		if score, err = strconv.Atoi(csvRecord[18]); err != nil {
			log.Errorf("LoadMarkers: unable to to understand int value from record %d value %s from %s with error %s", record, csvRecord[18], fileName, err)
			return err
		}
		if cAt, err := time.Parse(CSVTimestampFormat, csvRecord[21]); err != nil {
			log.Errorf("LoadMarkers: unable to to understand timestamp value from record %d value %s from %s with error %s", record, csvRecord[21], fileName, err)
			return err
		} else {
			createdAt = cAt
		}
		if uAt, err := time.Parse(CSVTimestampFormat, csvRecord[22]); err != nil {
			log.Errorf("LoadMarkers: unable to to understand timestamp value from record %d value %s from %s with error %s", record, csvRecord[22], fileName, err)
			return err
		} else {
			updatedAt = uAt
		}
		if clustered, err = strconv.ParseBool(csvRecord[23]); err != nil {
			log.Errorf("LoadMarkers: unable to to understand bool value from record %d value %s from %s with error %s", record, csvRecord[23], fileName, err)
			return err
		}
		embeddings := make(Embeddings, 1)
		embed := make(Embedding, 512)
		if err = json.Unmarshal([]byte(csvRecord[11]), &embed); err != nil {
			log.Errorf("unable to unmarshal(csvRecord[11]) %s", err)
		}
		embeddings[0] = embed

		markers[counter] = VectorMarker{
			MarkerUID:      csvRecord[0],
			FileUID:        csvRecord[1],
			MarkerType:     csvRecord[2],
			MarkerSrc:      csvRecord[3],
			MarkerReview:   markerReview,
			MarkerInvalid:  markerInvalid,
			SubjSrc:        csvRecord[8],
			FaceDist:       faceDist,
			EmbeddingsJSON: embeddings.JSON(),
			// Embedding:     DBEmbed{Embed: embedding},
			X:         x,
			Y:         y,
			W:         w,
			H:         h,
			Q:         q,
			Size:      size,
			Score:     score,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
			Clustered: clustered,
		}

		faceEmbeddings[counter] = VectorMarkerFace{
			MarkerUID:   markers[counter].MarkerUID,
			EmbeddingID: 0, // This is needed to support cases where there is more than 1 Embedding returned by the AI routines, but is out of scope for this
			Embedding:   DBEmbed{Embed: csvRecord[11]},
		}
		counter++
		record++
		if counter == batchsize {
			log.Infof("processing %d with counter %d", record, counter)
			if err = createDBMSMarkers(dsn.Driver, record, upto, &markers, &faceEmbeddings, log); err != nil {
				return err
			}
			upto += counter
			counter = 0
		}
	}

	if counter > 0 {
		markers = markers[:counter]
		return createDBMSMarkers(dsn.Driver, record, upto, &markers, &faceEmbeddings, log)
	}

	log.Infof("Load took %s", time.Since(start))

	return nil
}

// createDBMSMarkers uses Gorm to create the records in the database
func createDBMSMarkers(driver string, record, upto int, markers *[]VectorMarker, faceEmbeddings *[]VectorMarkerFace, log *logrus.Logger) (err error) {
	switch driver {
	case dbms.SQLite3:
		fallthrough
	case dbms.MySQL:
		fallthrough
	case dbms.Postgres:
		if err = dbms.Db().Create(&faceEmbeddings).Error; err != nil {
			log.Errorf("LoadMarkers: Create face embeddings record set up to %d failed with %s", record, err)
			return err
		}
		if err = dbms.Db().Create(&markers).Error; err != nil {
			log.Errorf("LoadMarkers: Create markers record set up to %d failed with %s", record, err)
			return err
		}
	case dbms.Qdrant:
		points := make([]*qdrant.PointStruct, len(*markers))
		var payload map[string]any
		for i, v := range *markers {
			e := (*faceEmbeddings)[i].Embedding.Embed
			embed32 := make(Embedding32, 512)
			if err = json.Unmarshal([]byte(e), &embed32); err != nil {
				log.Errorf("unable to unmarshal(e) %s", err)
			}
			if jsonBytes, err := json.Marshal(v); err != nil {
				log.Errorf("unable to marshal(v) %s", err)
			} else {
				if err = json.Unmarshal(jsonBytes, &payload); err != nil {
					log.Errorf("unable to unmashal(jsonBytes) %s", err)
				}
			}

			points[i] = &qdrant.PointStruct{
				Id:      qdrant.NewIDNum(uint64(upto + i)),
				Vectors: qdrant.NewVectorsDense(embed32),
				Payload: qdrant.NewValueMap(
					payload,
				),
			}
		}

		if r, err := dbms.QClient().Upsert(context.Background(), &qdrant.UpsertPoints{
			CollectionName: VectorMarker{}.TableName(),
			Points:         points,
		}); err != nil {
			log.Errorf("Upsert failed with %s", err)
		} else {
			log.Infof("Upsert result = %+v", r)
		}
	}
	return nil
}
