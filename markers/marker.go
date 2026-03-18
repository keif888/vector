package markers

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/big"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/keif888/vector/dbms"
	"github.com/keif888/vector/pkg/dsn"
	"github.com/qdrant/go-client/qdrant"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const (
	MarkerUnknown = ""
	MarkerFace    = "face"  // MarkerType for faces (implemented).
	MarkerLabel   = "label" // MarkerType for labels (todo).
)

// Faceless can be used as argument to match unmatched face markers.
var Faceless = []string{""}

const (
	// CharsetBase10 contains digits for base10 encoding.
	CharsetBase10 = "0123456789"
	// CharsetBase36 contains lowercase alphanumerics for base36.
	CharsetBase36 = "abcdefghijklmnopqrstuvwxyz0123456789"
	// CharsetBase62 contains mixed-case alphanumerics for base62.
	CharsetBase62 = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
)

// Supported metadata source strings.
const (
	SrcAuto    = ""        // Prio 1
	SrcDefault = "default" // Prio 1
	SrcMarker  = "marker"  // Prio 8
	SrcImage   = "image"   // Prio 8
)

var (
	// ClusterScoreThreshold is the minimum score required for faces that contribute to automatic clustering.
	ClusterScoreThreshold = 20
	// ClusterSizeThreshold is the minimum face size, in pixels, for faces considered when forming clusters.
	ClusterSizeThreshold = 60
	// ClusterDist is the similarity distance threshold that defines the cluster core.
	ClusterDist = 0.64
	// ClusterRadius is the maximum normalized distance for cluster samples.
	ClusterRadius = 0.42
	// MatchDist is the distance offset threshold used to match new faces with existing clusters.
	MatchDist = 0.4
	// CollisionDist is the minimum distance under which embeddings cannot be distinguished.
	CollisionDist = 0.05
	// ClusterCore is the minimum number of faces required to seed a cluster core.
	ClusterCore = 4
)

// CSV format constants
const (
	CSVTimestampFormat = "2006-01-02T15:04:05.000000000Z"
)

// Known MarkerUID for simple queries.
const (
	QueryMarkerUID = "mtbqjkz00jgjwufb"
)

// VectorMarkerFace represents the storage of face vectors
type VectorMarkerFace struct {
	MarkerUID   string  `gorm:"type:bytes;size:42;primaryKey;autoIncrement:false;"`
	EmbeddingID int     `gorm:"primaryKey"`
	Embedding   DBEmbed `gorm:"size:512;"`
}

// TableName returns the entity table name.
func (VectorMarkerFace) TableName() string {
	return "vector_marker_faces"
}

// VectorMarker represents an image marker point.
type VectorMarker struct {
	MarkerUID      string          `gorm:"type:bytes;size:42;primaryKey;autoIncrement:false;" json:"UID" yaml:"UID"`
	FileUID        string          `gorm:"type:bytes;size:42;index;default:'';" json:"FileUID" yaml:"FileUID"`
	MarkerType     string          `gorm:"type:bytes;size:8;default:'';" json:"Type" yaml:"Type"`
	MarkerSrc      string          `gorm:"type:bytes;size:8;default:'';" json:"Src" yaml:"Src,omitempty"`
	MarkerName     string          `gorm:"size:160;" json:"Name" yaml:"Name,omitempty"`
	MarkerReview   bool            `json:"Review" yaml:"Review,omitempty"`
	MarkerInvalid  bool            `json:"Invalid" yaml:"Invalid,omitempty"`
	SubjUID        string          `gorm:"type:bytes;size:42;index:idx_markers_subj_uid_src;" json:"SubjUID" yaml:"SubjUID,omitempty"`
	SubjSrc        string          `gorm:"type:bytes;size:8;index:idx_markers_subj_uid_src;default:'';" json:"SubjSrc" yaml:"SubjSrc,omitempty"`
	FaceID         string          `gorm:"type:bytes;size:64;index;" json:"FaceID" yaml:"FaceID,omitempty"`
	FaceDist       float64         `gorm:"default:-1;" json:"FaceDist" yaml:"FaceDist,omitempty"`
	EmbeddingsJSON json.RawMessage `gorm:"type:bytes;size:66666;" json:"-" yaml:"EmbeddingsJSON,omitempty"`
	embeddings     Embeddings      `gorm:"-" yaml:"-"`
	LandmarksJSON  json.RawMessage `gorm:"type:bytes;size:66666;" json:"-" yaml:"LandmarksJSON,omitempty"`
	X              float32         `json:"X" yaml:"X,omitempty"`
	Y              float32         `json:"Y" yaml:"Y,omitempty"`
	W              float32         `json:"W" yaml:"W,omitempty"`
	H              float32         `json:"H" yaml:"H,omitempty"`
	Q              int             `json:"Q" yaml:"Q,omitempty"`
	Size           int             `gorm:"default:-1;" json:"Size" yaml:"Size,omitempty"`
	Score          int             `gorm:"type:int;size:16;" json:"Score" yaml:"Score,omitempty"`
	Thumb          string          `gorm:"type:bytes;size:128;index;default:'';" json:"Thumb" yaml:"Thumb,omitempty"`
	MatchedAt      *time.Time      `sql:"index" json:"MatchedAt" yaml:"MatchedAt,omitempty"`
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ClusteredAt    *time.Time `sql:"index" json:"ClusteredAt" yaml:"ClusteredAt,omitempty"`
}

// TableName returns the entity table name.
func (VectorMarker) TableName() string {
	return "vector_markers"
}

// Face represents the face of a Subject.
type Face struct {
	ID              string          `gorm:"type:bytes;size:64;primaryKey;autoIncrement:false;" json:"ID" yaml:"ID"`
	FaceSrc         string          `gorm:"type:bytes;size:8;" json:"Src" yaml:"Src,omitempty"`
	FaceKind        int             `json:"Kind" yaml:"Kind,omitempty"`
	FaceHidden      bool            `json:"Hidden" yaml:"Hidden,omitempty"`
	SubjUID         string          `gorm:"type:bytes;size:42;index;default:'';" json:"SubjUID" yaml:"SubjUID,omitempty"`
	Samples         int             `json:"Samples" yaml:"Samples,omitempty"`
	SampleRadius    float64         `json:"SampleRadius" yaml:"SampleRadius,omitempty"`
	Collisions      int             `json:"Collisions" yaml:"Collisions,omitempty"`
	CollisionRadius float64         `json:"CollisionRadius" yaml:"CollisionRadius,omitempty"`
	MergeRetry      uint8           `gorm:"default:0" json:"-" yaml:"-"`
	MergeNotes      string          `gorm:"size:255;default:'';" json:"-" yaml:"-"`
	EmbeddingJSON   json.RawMessage `gorm:"type:bytes;size:66666;" json:"-" yaml:"EmbeddingJSON,omitempty"`
	embedding       Embedding       `gorm:"-" yaml:"-"`
	MatchedAt       *time.Time      `json:"MatchedAt" yaml:"MatchedAt,omitempty"`
	CreatedAt       time.Time       `json:"CreatedAt" yaml:"CreatedAt,omitempty"`
	UpdatedAt       time.Time       `json:"UpdatedAt" yaml:"UpdatedAt,omitempty"`
}

// Embedding represents a face embedding.
type Embedding []float64

// Embedding32 represents a face embedding in float32.
type Embedding32 []float32

// Embeddings represents a face embedding cluster.
type Embeddings []Embedding

// GenerateMarkers populates fileName with numberOfMarkers of randomly generated data
func GenerateMarkers(fileName string, numberOfMarkers int, log *logrus.Logger) (err error) {

	basePath := filepath.Dir(fileName)
	if err := os.MkdirAll(basePath, os.ModePerm); err != nil {
		log.Errorf("generateMarkers: unable to create required path %s with error %s", basePath, err)
		return err
	}
	var csvFile *os.File
	if csvFile, err = os.Create(fileName); err != nil {
		log.Errorf("generateMarkers: unable to create required file %s with error %s", fileName, err)
		return err
	}
	defer func() {
		if err := csvFile.Close(); err != nil {
			log.Errorf("generateMarkers: unable to close file %s with error %s", fileName, err)
		}
	}()

	csvWriter := csv.NewWriter(csvFile)
	defer csvWriter.Flush()

	csvHeader := []string{"marker_uid", "file_uid", "marker_type", "marker_src", "marker_name", "marker_review", "marker_invalid", "subj_uid", "subj_src", "face_id", "face_dist", "embedding", "x", "y", "w", "h", "q", "size", "score", "thumb", "matched_at", "created_at", "updated_at", "clsutered_at"}
	if err := csvWriter.Write(csvHeader); err != nil {
		log.Errorf("generateMarkers: unable to write header to file %s with error %s", fileName, err)
		return err
	}

	numberOfFaces := int(0.75 * float32(numberOfMarkers))
	log.Infof("generateMarkers: creating %d embeddings", numberOfFaces)
	sourceEmbeddings := make([]Embedding, numberOfFaces)
	jsonembed := make(Embedding, 512)
	var embedding Embedding
	for i := range numberOfFaces {
		for k := range 512 {
			if rand.IntN(2) == 0 { //nolint:gosec // test data generation crypto rand not required
				jsonembed[k] = rand.Float64() //nolint:gosec // test data generation crypto rand not required
			} else {
				jsonembed[k] = rand.Float64() * -1.0 //nolint:gosec // test data generation crypto rand not required
			}
		}
		normalizeEmbedding(jsonembed)
		sourceEmbeddings[i] = make(Embedding, 512)
		copy(sourceEmbeddings[i], jsonembed)
	}

	for i := range numberOfMarkers {
		if i%1000 == 0 && i > 0 {
			log.Infof("generateMarkers: writing %d of %d to %s", i, numberOfMarkers, fileName)
		}
		faceNumber := rand.IntN(numberOfFaces) //nolint:gosec // test data generation crypto rand not required
		embedding = sourceEmbeddings[faceNumber]
		for j := range 512 {
			embedding[j] = embedding[j] * (1 + (rand.Float64() * 2 * 0.0001) - 0.0001)
		}
		markerUID := GenerateUID('m')
		if i == 0 {
			markerUID = QueryMarkerUID // Guarantee that we know one MarkerUID
		}
		marker := VectorMarker{
			MarkerUID:     markerUID,
			FileUID:       GenerateUID('f'),
			MarkerType:    MarkerFace,
			MarkerSrc:     SrcImage,
			MarkerReview:  false,
			MarkerInvalid: false,
			SubjSrc:       SrcAuto,
			FaceDist:      rand.Float64(), //nolint:gosec // test data generation crypto rand not required
			// EmbeddingsJSON: []byte(embedding.JSON()),
			X:         rand.Float32(),      //nolint:gosec // test data generation crypto rand not required
			Y:         rand.Float32(),      //nolint:gosec // test data generation crypto rand not required
			W:         rand.Float32(),      //nolint:gosec // test data generation crypto rand not required
			H:         rand.Float32(),      //nolint:gosec // test data generation crypto rand not required
			Q:         rand.IntN(600),      //nolint:gosec // test data generation crypto rand not required
			Size:      rand.IntN(540) + 59, //nolint:gosec // test data generation crypto rand not required
			Score:     rand.IntN(130) + 19, //nolint:gosec // test data generation crypto rand not required
			CreatedAt: time.Now().UTC(),
			UpdatedAt: time.Now().UTC(),
		}

		csvRecord := []string{
			marker.MarkerUID,
			marker.FileUID,
			marker.MarkerType,
			marker.MarkerSrc,
			marker.MarkerName,
			strconv.FormatBool(marker.MarkerReview),
			strconv.FormatBool(marker.MarkerInvalid),
			marker.SubjUID,
			marker.SubjSrc,
			marker.FaceID,
			//fmt.Sprintf("%f", marker.FaceDist),
			strconv.FormatFloat(marker.FaceDist, 'f', 18, 64),
			embedding.JSON(),
			fmt.Sprintf("%f", marker.X),
			fmt.Sprintf("%f", marker.Y),
			fmt.Sprintf("%f", marker.W),
			fmt.Sprintf("%f", marker.H),
			strconv.Itoa(marker.Q),
			strconv.Itoa(marker.Size),
			strconv.Itoa(marker.Score),
			marker.Thumb,
			"", // MatchedAt
			marker.CreatedAt.Format(CSVTimestampFormat),
			marker.UpdatedAt.Format(CSVTimestampFormat),
			"", // ClusteredAt
		}
		if err := csvWriter.Write(csvRecord); err != nil {
			log.Errorf("generateMarkers: unable to write record %d to file %s with error %s", i, fileName, err)
			return err
		}

	}
	log.Infof("generateMarkers: wrote %d markers to %s", numberOfMarkers, fileName)
	return nil
}

// connectGormDB connects to the database
func connectGormDB(driver, dsn string) (db *dbms.DbConn, err error) {
	db = &dbms.DbConn{
		Driver: driver,
		Dsn:    dsn,
	}
	dbms.SetDbProvider(db)
	return
}

// setupGormDB performs initial cleanup for LoadMarkers
func setupGormDB(driver, dsn string) (db *dbms.DbConn, err error) {
	db, _ = connectGormDB(driver, dsn)

	if dbms.Db().Migrator().HasTable(&VectorMarker{}) {
		if err = dbms.Db().Migrator().DropTable(&VectorMarker{}); err != nil {
			return
		}
	}
	if dbms.Db().Migrator().HasTable(&VectorMarkerFace{}) {
		return db, dbms.Db().Migrator().DropTable(&VectorMarkerFace{})
	}
	return db, nil
}

// migrateVectorMarkers uses Gorm to create/alter the vector_marker table
func migrateVectorMarkers() (err error) {
	if err = dbms.Db().AutoMigrate(&VectorMarker{}); err != nil {
		return
	}

	// SQLite3 bombs out when attempting to migrate the table as it can't understand the virtual table.
	if dbms.DbDialect() != dbms.SQLite3 {
		return dbms.Db().AutoMigrate(&VectorMarkerFace{})
	}
	return
}

// connectQdrant connects to the database
func connectQdrant(dsn dsn.DSN) {
	conn := &dbms.QConn{
		Dsn: dsn,
	}

	dbms.SetClientProvider(conn)
}

// setupQdrantDB uses the Qdrant client to connect and performs initial cleanup for LoadMarkers
func setupQdrantDB(dsn dsn.DSN) (err error) {
	connectQdrant(dsn)

	if ok, err := dbms.QClient().CollectionExists(context.Background(), VectorMarker{}.TableName()); err != nil {
		log.Errorf("setupQdrantDB: CollectionExists failed with %s", err)
		return err
	} else {
		if ok {
			if err := dbms.QClient().DeleteCollection(context.Background(), VectorMarker{}.TableName()); err != nil {
				log.Errorf("setupQdrantDB: DeleteCollection failed with %s", err)
				return err
			}
		}
	}

	// Looks like Qdrant doesn't need to split this out.
	// if ok, err := dbms.QClient().CollectionExists(context.Background(), VectorMarkerFace{}.TableName()); err != nil {
	// 	log.Errorf("setupQdrantDB: CollectionExists failed with %s", err)
	// 	return err
	// } else {
	// 	if ok {
	// 		if err := dbms.QClient().DeleteCollection(context.Background(), VectorMarkerFace{}.TableName()); err != nil {
	// 			log.Errorf("setupQdrantDB: DeleteCollection failed with %s", err)
	// 			return err
	// 		}
	// 	}
	// }
	return nil
}

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
			if err = dbms.Db().Exec("CREATE INDEX ON vector_marker_faces USING hnsw (embedding vector_cosine_ops) WITH (m=8)").Error; err != nil {
				log.Errorf("LoadMarkers: vector_marker_faces index setup failed with %s", err)
				return err
			}
		} else if DistanceEquation(equation) == Distance_Euclidean {
			if err = dbms.Db().Exec("CREATE INDEX ON vector_marker_faces USING hnsw (embedding vector_l2_ops) WITH (m=8)").Error; err != nil {
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
			FieldName:      "FaceID",
			FieldType:      qdrant.FieldType_FieldTypeKeyword.Enum(),
		}); err != nil {
			log.Errorf("LoadMarkers: CreateFieldIndex FaceID failed with %s", err)
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

func QueryMarkers(dataSourceName dsn.DSN, markerUID string, equation int, log *logrus.Logger) (err error) {
	if equation != int(Distance_Euclidean) && equation != int(Distance_Cosine) {
		return fmt.Errorf("equation %d was not valid", equation)
	}
	start := time.Now()
	switch dataSourceName.Driver {
	case dsn.DriverMySQL, dsn.DriverPostgres, dsn.DriverSQLite3:
		var db *dbms.DbConn

		if db, err = connectGormDB(dataSourceName.Driver, dataSourceName.ToString()); err != nil {
			db.Close()
			log.Errorf("LoadMarkers: database setup failed with %s", err)
			return err
		}
		defer db.Close()

		var e string
		if m, err := gorm.G[VectorMarkerFace](dbms.Db()).Where("marker_uid = ?", markerUID).First(context.Background()); err != nil {
			log.Errorf("LoadMarkers: VectorMarkerFace first failed with %s", err)
			return err
		} else {
			e = m.Embedding.Embed
		}

		var c int64

		if c, err = gorm.G[VectorMarkerFace](dbms.Db()).Where(DBEmbedQuery("embedding").Equals(DistanceEquation(equation), 0.0, e)).Count(context.Background(), "*"); err != nil {
			log.Errorf("LoadMarkers: count failed with %s", err)
			return err
		} else {
			log.Infof("marker count = %d", c)
		}
		if c, err = gorm.G[VectorMarkerFace](dbms.Db()).Where(DBEmbedQuery("embedding").LessThanOrEquals(DistanceEquation(equation), 0.93, e)).Count(context.Background(), "*"); err != nil {
			log.Errorf("LoadMarkers: count failed with %s", err)
			return err
		} else {
			log.Infof("marker count = %d", c)
		}
		/*
			// Old code as examples of coding to get data out of SQLite...
				if driver == dbms.SQLite3 {
					log.Infof("LoadMarkers: e = %s", e)
					idf, _ := strconv.ParseFloat(e, 64)
					id := int64(idf)
					if v, err := gorm.G[VectorMarkerItems](dbms.Db()).Where("rowid = ?", id).First(context.Background()); err != nil {
						log.Errorf("LoadMarkers: sqlite get id failed with %s", err)
						return err
					} else {
						e = v.Embedding.Embed
					}

					type Result struct {
						Rowid    int
						Distance float64
					}

					var r []Result

					if err = gorm.G[Result](dbms.Db()).
						Table("vector_marker_items").
						Select("rowid, distance").
						Where("embedding match ? and k = ? and distance = ?", e, 5, 0).
						Scan(context.Background(), &r); err != nil {
						log.Errorf("LoadMarkers: sqlite rowid distance failed with %s", err)
						return err
					}
					log.Infof("LoadMarkers: result1 = %+v", r)

					if err = gorm.G[VectorMarkerItems](dbms.Db()).
						Select("rowid, distance").
						Where(DBEmbedQuery("embedding").Equals(0.0, e)).
						Scan(context.Background(), &r); err != nil {
						log.Errorf("LoadMarkers: sqlite rowid distance failed with %s", err)
						return err
					}
					log.Infof("LoadMarkers: result2 = %+v", r)

					result := gorm.WithResult()
					if err = gorm.G[any](dbms.Db(), result).Exec(context.Background(), "select count(*) FROM `vector_marker_items` WHERE `embedding` match ? AND k = 5 AND distance = 0", e); err != nil {
						log.Errorf("LoadMarkers: any exec failed with %s", err)
						return err
					}
					log.Infof("LoadMarkers: any = %+v, %d, %+v", result, result.RowsAffected, result.Result)

					var c int64

					if c, err = gorm.G[VectorMarkerItems](dbms.Db()).Where(DBEmbedQuery("embedding").Equals(0.0, e)).Count(context.Background(), "*"); err != nil {
						log.Errorf("LoadMarkers: count failed with %s", err)
						return err
					} else {
						log.Infof("marker count = %d", c)
					}
				} else {
					var c int64

					if c, err = gorm.G[VectorMarker](dbms.Db()).Where(DBEmbedQuery("embedding").Equals(0.0, e)).Count(context.Background(), "*"); err != nil {
						log.Errorf("LoadMarkers: count failed with %s", err)
						return err
					} else {
						log.Infof("marker count = %d", c)
					}
				}
		*/
	case dsn.DriverQdrant:
		connectQdrant(dataSourceName)
		defer func() {
			if err = dbms.QClient().Close(); err != nil {
				log.Errorf("QueryMarkers: Client Close failed with %s", err)
			}
		}()

		var e Embedding32
		if result, err := dbms.QClient().Scroll(context.Background(), &qdrant.ScrollPoints{
			CollectionName: VectorMarker{}.TableName(),
			Filter: &qdrant.Filter{
				Must: []*qdrant.Condition{
					qdrant.NewMatch("UID", markerUID),
				},
			},
			WithPayload: qdrant.NewWithPayload(true),
			WithVectors: qdrant.NewWithVectors(true),
		}); err != nil {
			log.Errorf("QueryMarkers: Query of UID=%s failed with %s", markerUID, err)
			return err
		} else {
			for _, r := range result {
				log.Infof("MarkerUID = %s", r.Payload["UID"].GetStringValue())
				v := r.Vectors.GetVector()
				//v1 := v.GetMultiDense()
				//e = v1.Vectors[0].Data
				v1 := v.GetDense()
				e = v1.Data
				// log.Infof("%+v", e)
				// log.Infof("%s", e.JSON())
				break
			}
		}

		// if result, err := dbms.QClient().Get(context.Background(), &qdrant.GetPoints{
		// 	CollectionName: VectorMarker{}.TableName(),
		// 	Ids: []*qdrant.PointId{
		// 		qdrant.NewIDNum(336),
		// 	},
		// 	WithPayload: qdrant.NewWithPayload(true),
		// 	WithVectors: qdrant.NewWithVectors(true),
		// }); err != nil {
		// 	log.Errorf("QueryMarkers: Query of Id=336 failed with %s", err)
		// 	return err
		// } else {
		// 	// log.Infof("%+v", result)
		// 	for _, r := range result {
		// 		log.Infof("MarkerUID = %s", r.Payload["UID"].GetStringValue())
		// 		v := r.Vectors.GetVector()
		// 		//v1 := v.GetMultiDense()
		// 		//e = v1.Vectors[0].Data
		// 		v1 := v.GetDense()
		// 		e = v1.Data
		// 		// log.Infof("%+v", e)
		// 		// log.Infof("%s", e.JSON())
		// 		break
		// 	}
		// }

		// Count only works against named vectors or payload.
		/*
			exact := true
			if c, err := dbms.QClient().Count(context.Background(), &qdrant.CountPoints{
				CollectionName: VectorMarker{}.TableName(),
				Exact:          &exact,
				Filter: &qdrant.Filter{
					Should: []*qdrant.Condition{
						qdrant.NewHasVector(e.JSON()),
					},
				},
			}); err != nil {
				log.Errorf("QueryMarkers: Count failed with %s", err)
				return err
			} else {
				log.Infof("marker count = %d", c)
			}
		*/

		/*
			// This returns the 1st 10 that have a score > than the one specified.
			// Which is not what we want.  (and 10 is a default limit which can be adjusted in the query)
			score := float32(0.0)
			if result, err := dbms.QClient().Query(context.Background(), &qdrant.QueryPoints{
				CollectionName: VectorMarker{}.TableName(),
				Query:          qdrant.NewQueryDense(e),
				ScoreThreshold: &score,
				WithPayload:    qdrant.NewWithPayload(true),
				WithVectors:    qdrant.NewWithVectors(true),
			}); err != nil {
				log.Errorf("QueryMarkers: Query of Id=5 failed with %s", err)
				return err
			} else {
				log.Infof("Search found %d results", len(result))
				for _, r := range result {
					// e = r.Vectors.String()
					log.Infof("Id = %+v, Score = %f", r.Id, r.Score)
				}
			}
		*/
		score := e[0]
		score = float32(0.9999995)
		limit := uint64(10)
		// Return up to 10 results, with full data
		if result, err := dbms.QClient().Query(context.Background(), &qdrant.QueryPoints{
			CollectionName: VectorMarker{}.TableName(),
			Query:          qdrant.NewQueryDense(e), // 4 results
			// Query: qdrant.NewQueryNearest(qdrant.NewVectorInputDense(e)), // 4 results
			// Query:          qdrant.NewQueryID(qdrant.NewIDNum(336)), // 3 results (missing 336)
			Limit:          &limit,
			ScoreThreshold: &score,
			WithPayload:    qdrant.NewWithPayload(true),
			WithVectors:    qdrant.NewWithVectors(true),
		}); err != nil {
			log.Errorf("QueryMarkers: Query of Id=336 failed with %s", err)
			return err
		} else {
			log.Infof("Search found %d results", len(result))
			for _, r := range result {
				// e = r.Vectors.String()
				log.Infof("Id = %+v, Score = %f, MarkerUID = %s", r.Id, r.Score, r.Payload["UID"].GetStringValue())
			}
		}
		// Simulate Count
		score = float32(0.07)
		limit = uint64(100000)
		if result, err := dbms.QClient().Query(context.Background(), &qdrant.QueryPoints{
			CollectionName: VectorMarker{}.TableName(),
			Query:          qdrant.NewQueryDense(e), // 4 results
			// Query: qdrant.NewQueryNearest(qdrant.NewVectorInputDense(e)), // 4 results
			// Query:          qdrant.NewQueryID(qdrant.NewIDNum(336)), // 3 results (missing 336)
			Limit:          &limit,
			ScoreThreshold: &score,
			WithPayload:    qdrant.NewWithPayload(false),
			WithVectors:    qdrant.NewWithVectors(false),
		}); err != nil {
			log.Errorf("QueryMarkers: Query of Id=336 failed with %s", err)
			return err
		} else {
			log.Infof("Search found %d results", len(result))
		}

	}
	log.Infof("Query took %s", time.Since(start))
	return
}

// QueryMatchMarkers aims to simulate the face.MatchMarkers with an empty faces id.
func QueryMatchMarkers(dataSourceName dsn.DSN, markerUID string, equation int, bruteForce bool, log *logrus.Logger) (err error) {
	if equation != int(Distance_Euclidean) && equation != int(Distance_Cosine) {
		return fmt.Errorf("equation %d was not valid", equation)
	}
	start := time.Now()
	switch dataSourceName.Driver {
	case dsn.DriverSQLite3:
		var db *dbms.DbConn

		if db, err = connectGormDB(dataSourceName.Driver, dataSourceName.ToString()); err != nil {
			db.Close()
			log.Errorf("LoadMarkers: database setup failed with %s", err)
			return err
		}
		defer db.Close()

		var faceEmbedding string
		if m, err := gorm.G[VectorMarkerFace](dbms.Db()).Where("marker_uid = ?", markerUID).First(context.Background()); err != nil {
			log.Errorf("LoadMarkers: VectorMarkerFace first failed with %s", err)
			return err
		} else {
			faceEmbedding = m.Embedding.Embed
		}

		if len(faceEmbedding) < 1 {
			log.Errorf("faceEmbedding not populated")
			return nil
		}

		var dist float64
		var m string
		dist = -1

		// MatchDist + ClusterRadius is the worst case scenario for a face (m.SampleRadius + face.MatchDist).
		// Use that as a 1st pass cleanser, then apply the switch clause

		/*
				// Can't get Generics to allow these queries to work :-(
			_, _ = gorm.G[VectorMarker](dbms.Db()).
				//			Build(clause.From.Tables[{Table(VectorMarkerFace{}.TableName()), Table(VectorMarker{}.TableName())}]).
				//Table(VectorMarkerFace{}.TableName()).
				// Joins(clause.InnerJoin.Association(VectorMarkerFace{}.TableName()).As("ASASAS"), func(db gorm.JoinBuilder, joinTable clause.Table, curTable clause.Table) error {
				// 	db.Where("?.marker_uid = ?.marker_uid", joinTable, curTable)
				// 	return nil
				// }).
				Table(VectorMarker{}.TableName()).
				//Table(VectorMarkerFace{}.TableName()).
				Joins(clause.JoinTarget{
					Type:        clause.InnerJoin,
					Association: "vector_marker_faces",
					Subquery: clause.Join{
						Type:  clause.InnerJoin,
						Table: clause.Table{Name: "vector_markers"},
						ON: clause.Where{
							Exprs: []clause.Expression{
								clause.Eq{
									Column: clause.Column{Table: "vector_markers", Name: "marker_uid"},
									Value:  clause.Column{Table: "vector_marker_faces", Name: "marker_uid"},
								},
							},
						},
					} , Table: "vector_marker_faces"}, func(db gorm.JoinBuilder, joinTable clause.Table, curTable clause.Table) error {
					db.Where("?.marker_uid = ?.marker_uid", joinTable.Name, curTable.Name)
					return nil
				}).
				// Joins(clause.JoinTarget{Type: clause.InnerJoin, Association: "vector_markers", Table: "vector_markers"}, func(db gorm.JoinBuilder, joinTable clause.Table, curTable clause.Table) error {
				// 	db.Where("?.marker_uid = ?.marker_uid", joinTable.Name, curTable.Name)
				//	return nil
				// }).
				// Joins(clause.InnerJoin.Association(VectorMarker{}.TableName()), func(db gorm.JoinBuilder, joinTable clause.Table, curTable clause.Table) error {
				// 	db.Where("?.marker_uid = ?.marker_uid", joinTable, curTable)
				// 	return nil
				// }).
				// Where("marker_uid <> ?", markerUID).
				Where("vector_markers.marker_uid = vector_marker_faces.marker_uid").
				Rows(context.Background())

			if rows, err := gorm.G[SillyType](dbms.Db()).
				//Table(VectorMarkerFace{}.TableName()).
				// Joins(clause.CrossJoin.Association(VectorMarker{}.TableName()), func(db gorm.JoinBuilder, joinTable clause.Table, curTable clause.Table) error {
				// 	db.Where("?.marker_uid = ?.marker_uid", joinTable, curTable)
				// 	return nil
				// }).
				// Joins(clause.CrossJoin.Association(VectorMarkerFace{}.TableName()), func(db gorm.JoinBuilder, joinTable clause.Table, curTable clause.Table) error {
				// 	db.Where("?.marker_uid = ?.marker_uid", joinTable, curTable)
				// 	return nil
				// }).
				Where("marker_uid <> ?", markerUID).
				//Where(DBEmbedQuery("embedding").
				//	LessThanOrEquals(MatchDist+ClusterRadius, faceEmbedding)).
				//Joins("inner join vector_markers on vector_markers.marker_uid = vector_marker_faces.marker_uid").
				// Where("marker_invalid = FALSE AND marker_type = ? AND face_id IN (?)", MarkerFace, Faceless).
				// Order("distance").
				// Select("?, vector_marker_faces.marker_uid", DBEmbedQuery("embedding").Distance(faceEmbedding, "distance")).
				Rows(context.Background()); err != nil {
		*/
		if bruteForce {
			var markers []VectorMarker
			markers, err := gorm.G[VectorMarker](dbms.Db()).Where("marker_invalid = FALSE AND marker_type = ? AND face_id IN (?)", MarkerFace, Faceless).Find(context.Background())

			if err != nil {
				log.Debugf("faces: failed fetching markers matching face id %s (%s)", strings.Join(Faceless, ", "), err)
				return err
			}

			resultLen := len(markers)
			log.Infof("MatchMarkers: found %d markers", resultLen)
			embed := make(Embedding, 512)
			if err = json.Unmarshal([]byte(faceEmbedding), &embed); err != nil {
				log.Errorf("unable to unmarshal(e) %s", err)
			}
			f := Face{EmbeddingJSON: json.RawMessage(embed.JSON())}
			startl := time.Now()
			for i, marker := range markers {
				if time.Since(startl) > time.Duration(time.Minute*15) {
					log.Infof("faces: matching %d of %d markers", i, resultLen)
					startl = time.Now()
				}
				if ok, dist := f.Match(marker.Embeddings()); !ok {
					// Ignore.
					//} else if _, err = marker.SetFace(m, dist); err != nil {
					//					return err
				} else {
					log.Infof("Distance was %f for MarkerUID %s", dist, marker.MarkerUID)
				}
			}

		} else {
			if rows, err := dbms.Db().
				Model(&VectorMarker{}).
				Where("vector_markers.marker_uid <> ?", markerUID).
				Joins("INNER JOIN vector_marker_faces ON vector_markers.marker_uid = vector_marker_faces.marker_uid").
				Where(DBEmbedQuery("embedding").
					LessThanOrEquals(DistanceEquation(equation), MatchDist+ClusterRadius, faceEmbedding),
				).
				Where("marker_invalid = FALSE AND marker_type = ? AND face_id IN (?)", MarkerFace, Faceless).
				Select("?, vector_marker_faces.marker_uid", DBEmbedQuery("embedding").Distance(DistanceEquation(equation), faceEmbedding, "distance")).
				Order("distance").
				Rows(); err != nil {
				log.Errorf("QueryMatch: Select failed with %s", err)
				return err
			} else {
				for rows.Next() {
					if err = rows.Scan(&dist, &m); err != nil {
						log.Errorf("QueryMatch: Rows.Scan failed with %s", err)
						return err
					}
					switch {
					case dist < 0:
						// Should never happen.
						log.Warnf("Distance %f was less than 0.", dist)
					case dist > MatchDist+ClusterRadius: // (m.SampleRadius + face.MatchDist)
						// Too far.
						log.Infof("Distance %f was greater than allowed.", dist)
					// case m.CollisionRadius > CollisionDist && dist > m.CollisionRadius:
					// Dont' have a face to be able to do this test.
					// Within radius of reported collisions.
					// return false, dist
					default:
						log.Infof("Distance was %f for MarkerUID %s", dist, m)
						// Get the marker by UID
						// marker.SetFace()
					}

				}
				if err = rows.Close(); err != nil {
					log.Errorf("QueryMatch: Rows.Close failed with %s", err)
					return err
				}
			}
		}

	case dsn.DriverMySQL, dsn.DriverPostgres:
		var db *dbms.DbConn

		if db, err = connectGormDB(dataSourceName.Driver, dataSourceName.ToString()); err != nil {
			db.Close()
			log.Errorf("LoadMarkers: database setup failed with %s", err)
			return err
		}
		defer db.Close()

		var faceEmbedding string
		if m, err := gorm.G[VectorMarkerFace](dbms.Db()).Where("marker_uid = ?", markerUID).First(context.Background()); err != nil {
			log.Errorf("LoadMarkers: VectorMarkerFace first failed with %s", err)
			return err
		} else {
			faceEmbedding = m.Embedding.Embed
		}

		if len(faceEmbedding) < 1 {
			log.Errorf("faceEmbedding not populated")
			return nil
		}

		if bruteForce {
			var markers []VectorMarker
			markers, err := gorm.G[VectorMarker](dbms.Db()).Where("marker_invalid = FALSE AND marker_type = ? AND face_id IN (?)", MarkerFace, Faceless).Find(context.Background())

			if err != nil {
				log.Debugf("faces: failed fetching markers matching face id %s (%s)", strings.Join(Faceless, ", "), err)
				return err
			}

			resultLen := len(markers)
			log.Infof("MatchMarkers: found %d markers", resultLen)
			embed := make(Embedding, 512)
			if err = json.Unmarshal([]byte(faceEmbedding), &embed); err != nil {
				log.Errorf("unable to unmarshal(e) %s", err)
			}
			f := Face{EmbeddingJSON: json.RawMessage(embed.JSON())}
			startl := time.Now()
			for i, marker := range markers {
				if time.Since(startl) > time.Duration(time.Minute*15) {
					log.Infof("faces: matching %d of %d markers", i, resultLen)
					startl = time.Now()
				}
				if ok, dist := f.Match(marker.Embeddings()); !ok {
					// Ignore.
					//} else if _, err = marker.SetFace(m, dist); err != nil {
					//					return err
				} else {
					log.Infof("Distance was %f for MarkerUID %s", dist, marker.MarkerUID)
				}
			}

		} else {

			// MatchDist + ClusterRadius is the worst case scenario for a face (m.SampleRadius + face.MatchDist).
			// Use that as a 1st pass cleanser, then apply the switch clause

			// DistResult is used to capture the result from the distance query
			type DistResult struct {
				Distance  float64
				MarkerUID string
			}

			var distResults []DistResult
			if result := dbms.Db().
				Model(&VectorMarker{}).
				Where("vector_markers.marker_uid <> ?", markerUID).
				Joins("INNER JOIN vector_marker_faces ON vector_markers.marker_uid = vector_marker_faces.marker_uid").
				Where(DBEmbedQuery("embedding").
					LessThanOrEquals(DistanceEquation(equation), MatchDist+ClusterRadius, faceEmbedding),
				).
				Where("marker_invalid = FALSE AND marker_type = ? AND face_id IN (?)", MarkerFace, Faceless).
				Select("?, vector_marker_faces.marker_uid", DBEmbedQuery("embedding").Distance(DistanceEquation(equation), faceEmbedding, "distance")).
				Order("distance").
				Find(&distResults); result.Error != nil {
				log.Errorf("QueryMatchMarkers: Select failed with %s", result.Error)
				return result.Error
			} else {
				for _, r := range distResults {
					switch {
					case r.Distance < 0:
						// Should never happen.
						log.Warnf("Distance %f was less than 0.", r.Distance)
					case r.Distance > MatchDist+ClusterRadius: // (m.SampleRadius + face.MatchDist)
						// Too far.
						log.Infof("Distance %f was greater than allowed.", r.Distance)
					// case m.CollisionRadius > CollisionDist && r.Distance > m.CollisionRadius:
					// Dont' have a face to be able to do this test.
					// Within radius of reported collisions.
					// return false, r.Distance
					default:
						log.Infof("Distance was %f for MarkerUID %s", r.Distance, r.MarkerUID)
						// Get the marker by UID
						// marker.SetFace()
					}
				}
			}
		}

	case dsn.DriverQdrant:
		connectQdrant(dataSourceName)
		defer func() {
			if err = dbms.QClient().Close(); err != nil {
				log.Errorf("QueryMarkers: Client Close failed with %s", err)
			}
		}()

		var e Embedding32
		if result, err := dbms.QClient().Scroll(context.Background(), &qdrant.ScrollPoints{
			CollectionName: VectorMarker{}.TableName(),
			Filter: &qdrant.Filter{
				Must: []*qdrant.Condition{
					qdrant.NewMatch("UID", markerUID),
				},
			},
			WithPayload: qdrant.NewWithPayload(true),
			WithVectors: qdrant.NewWithVectors(true),
		}); err != nil {
			log.Errorf("QueryMarkers: Query of UID=%s failed with %s", markerUID, err)
			return err
		} else {
			for _, r := range result {
				log.Infof("MarkerUID = %s", r.Payload["UID"].GetStringValue())
				v := r.Vectors.GetVector()
				v1 := v.GetDense()
				e = v1.Data
				break
			}
		}

		score := float32(1.0 - (MatchDist + ClusterRadius))
		limit := uint64(2000000)
		// Return up to 100000 results, with full data
		if results, err := dbms.QClient().Query(context.Background(), &qdrant.QueryPoints{
			CollectionName: VectorMarker{}.TableName(),
			Query:          qdrant.NewQueryDense(e),
			Filter: &qdrant.Filter{
				MustNot: []*qdrant.Condition{
					qdrant.NewMatch("UID", markerUID),
				},
				Must: []*qdrant.Condition{
					qdrant.NewMatchBool("Invalid", false),
					qdrant.NewMatch("Type", MarkerFace),
					qdrant.NewMatchKeywords("FaceID", Faceless...),
				},
			},
			Limit:          &limit,
			ScoreThreshold: &score,
			WithPayload:    qdrant.NewWithPayload(true),
			WithVectors:    qdrant.NewWithVectors(false),
		}); err != nil {
			log.Errorf("QueryMarkers: Query for matches failed with %s", err)
			return err
		} else {
			for _, result := range results {
				dist := result.Score
				m := result.Payload["UID"].GetStringValue()
				switch {
				case dist < -1.00001 || dist > 1.00001:
					// Should never happen.
					log.Warnf("Distance %f was outside range of -1.0 to 1.0 .", dist)
				case equation == int(Distance_Euclidean) && dist > float32(MatchDist+ClusterRadius): // (m.SampleRadius + face.MatchDist)
					// Too far.
					log.Infof("Distance %f was greater than allowed.", dist)

				case equation == int(Distance_Cosine) && dist < float32(1-(MatchDist+ClusterRadius)): // (m.SampleRadius + face.MatchDist)
					// Too far.
					log.Infof("Distance %f was less than allowed.", dist)
				// case m.CollisionRadius > CollisionDist && dist > m.CollisionRadius:
				// Dont' have a face to be able to do this test.
				// Within radius of reported collisions.
				// return false, dist
				default:
					log.Infof("Distance was %f for MarkerUID %s", dist, m)
				}

			}
		}
	}
	log.Infof("Query took %s", time.Since(start))
	return
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

// normalizeEmbedding is something that PhotoPrism uses, so it's replicated here.
func normalizeEmbedding(e Embedding) {
	var sum float64

	for _, v := range e {
		sum += v * v
	}

	if sum == 0 {
		return
	}

	inv := 1 / math.Sqrt(sum)

	for i := range e {
		e[i] *= inv
	}
}

// GenerateUID returns a unique id with prefix as string.
func GenerateUID(prefix byte) string {
	return generateUID(prefix, time.Now())
}

// generateUID returns a unique id with prefix as string at a given time.
func generateUID(prefix byte, t time.Time) string {
	result := make([]byte, 0, 16)
	result = append(result, prefix)
	result = append(result, strconv.FormatInt(t.UTC().Unix(), 36)[0:6]...)
	result = append(result, Base36(9)...)

	return string(result)
}

// Base36 generates a random token containing lowercase letters and numbers.
func Base36(length int) string {
	return Charset(length, CharsetBase36)
}

// Charset generates a random token with the specified length and charset.
func Charset(length int, charset string) string {
	if length < 1 {
		return ""
	} else if length > 4096 {
		length = 4096
	}

	m := big.NewInt(int64(len(charset)))
	b := make([]byte, length)

	for i := range b {
		if r, err := crand.Int(crand.Reader, m); err == nil {
			b[i] = charset[r.Int64()]
		}
	}

	return string(b)
}

// JSON returns the Embedding as a formatted JSON string
func (e Embedding) JSON() string {

	var noResult = ""

	if len(e) < 1 {
		return noResult
	}

	if result, err := json.Marshal(e); err != nil {
		return noResult
	} else {
		return string(result)
	}
}

// JSON returns the Embedding as a formatted JSON string
func (e Embedding32) JSON() string {

	var noResult = ""

	if len(e) < 1 {
		return noResult
	}

	if result, err := json.Marshal(e); err != nil {
		return noResult
	} else {
		return string(result)
	}
}

// JSON returns the embeddings as JSON-encoded bytes.
func (embeddings Embeddings) JSON() []byte {
	var noResult = []byte("")

	if embeddings.Empty() {
		return noResult
	}

	if result, err := json.Marshal(embeddings); err != nil {
		return noResult
	} else {
		return result
	}
}

// MariaDBEmbedding encodes an Embedding in the native MariaDB format (set of IEEE 754 floating point numbers)
func MariaDBEmbedding(values Embedding) (result []byte) {
	result = make([]byte, len(values)*4)
	for i, value := range values {
		words := math.Float32bits(float32(value))
		binary.LittleEndian.PutUint32(result[i*4:], words)
	}
	return
}

// Embeddings returns parsed marker embeddings.
func (m *VectorMarker) Embeddings() Embeddings {
	if len(m.EmbeddingsJSON) == 0 {
		return Embeddings{}
	} else if len(m.embeddings) > 0 {
		return m.embeddings
	} else if err := json.Unmarshal(m.EmbeddingsJSON, &m.embeddings); err != nil {
		log.Errorf("markers: %s while parsing embeddings json", err)
	}

	return m.embeddings
}

// Embedding returns parsed face embedding.
func (m *Face) Embedding() Embedding {
	if len(m.EmbeddingJSON) == 0 {
		return Embedding{}
	} else if len(m.embedding) > 0 {
		return m.embedding
	} else if err := json.Unmarshal(m.EmbeddingJSON, &m.embedding); err != nil {
		log.Errorf("failed parsing face embedding json: %s", err)
	}

	return m.embedding
}

// Match tests if embeddings match this face.
func (m *Face) Match(embeddings Embeddings) (match bool, dist float64) {
	dist = -1

	if embeddings.Empty() {
		// Np embeddings, no match.
		return false, dist
	}

	faceEmbedding := m.Embedding()

	if len(faceEmbedding) == 0 {
		// Should never happen.
		return false, dist
	}

	// Calculate the smallest distance to embeddings.
	for _, e := range embeddings {
		if d := e.Dist(faceEmbedding); d < dist || dist < 0 {
			dist = d
		}
	}

	// Any reasons embeddings do not match this face?
	switch {
	case dist < 0:
		// Should never happen.
		return false, dist
	case dist > MatchDist+ClusterRadius: // (m.SampleRadius + face.MatchDist)
		//	case dist > (m.SampleRadius + MatchDist):
		// Too far.
		return false, dist
		//	case m.CollisionRadius > CollisionDist && dist > m.CollisionRadius:
		// Within radius of reported collisions.
		//		return false, dist
	}

	// If not, at least one of the embeddings match!
	return true, dist
}

// Empty tests if embeddings are empty.
func (embeddings Embeddings) Empty() bool {
	if len(embeddings) < 1 {
		return true
	}

	return len(embeddings[0]) < 1
}

// Count returns the number of embeddings.
func (embeddings Embeddings) Count() int {
	if embeddings.Empty() {
		return 0
	}

	return len(embeddings)
}

// Dist calculates the distance to another face embedding.
func (m Embedding) Dist(other Embedding) float64 {
	if len(other) == 0 || len(m) != len(other) {
		return -1
	}

	var sum float64

	for i, value := range m {
		diff := value - other[i]
		sum += diff * diff
	}

	return math.Sqrt(sum)
}
