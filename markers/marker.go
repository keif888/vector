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
	"time"

	"github.com/keif888/vector/dbms"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const (
	MarkerUnknown = ""
	MarkerFace    = "face"  // MarkerType for faces (implemented).
	MarkerLabel   = "label" // MarkerType for labels (todo).
)

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

// CSV format constants
const (
	CSVTimestampFormat = "2006-01-02T15:04:05.000000000Z"
)

// VectorMarkerFace represents the storage of face vectors
type VectorMarkerFace struct {
	MarkerUID   string  `gorm:"type:bytes;size:42;primaryKey;autoIncrement:false;"`
	EmbeddingID int     `gorm:"primaryKey"`
	Embedding   DBEmbed `gorm:"size:512;"`
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
}

// Embedding represents a face embedding.
type Embedding []float64

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

	csvHeader := []string{"marker_uid", "file_uid", "marker_type", "marker_src", "marker_name", "marker_review", "marker_invalid", "subj_uid", "subj_src", "face_id", "face_dist", "embedding", "x", "y", "w", "h", "q", "size", "score", "thumb", "matched_at", "created_at", "updated_at"}
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
		faceNumber := rand.IntN(numberOfFaces)
		embedding = sourceEmbeddings[faceNumber] //nolint:gosec // test data generation crypto rand not required
		marker := VectorMarker{
			MarkerUID:     GenerateUID('m'),
			FileUID:       GenerateUID('f'),
			MarkerType:    MarkerFace,
			MarkerSrc:     SrcImage,
			MarkerReview:  false,
			MarkerInvalid: false,
			SubjSrc:       SrcAuto,
			FaceDist:      rand.Float64(), //nolint:gosec // test data generation crypto rand not required
			// EmbeddingsJSON: []byte(embedding.JSON()),
			X:         rand.Float32(), //nolint:gosec // test data generation crypto rand not required
			Y:         rand.Float32(), //nolint:gosec // test data generation crypto rand not required
			W:         rand.Float32(), //nolint:gosec // test data generation crypto rand not required
			H:         rand.Float32(), //nolint:gosec // test data generation crypto rand not required
			Q:         rand.IntN(600), //nolint:gosec // test data generation crypto rand not required
			Size:      rand.IntN(600), //nolint:gosec // test data generation crypto rand not required
			Score:     rand.IntN(150), //nolint:gosec // test data generation crypto rand not required
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
			"", //
			marker.CreatedAt.Format(CSVTimestampFormat),
			marker.UpdatedAt.Format(CSVTimestampFormat),
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

// LoadMarkers retreives the saved markers from fileName and loads them into the table
func LoadMarkers(fileName, driver, dsn string, log *logrus.Logger) (err error) {
	var db *dbms.DbConn
	switch driver {
	case dbms.MySQL:
		if db, err = setupGormDB(driver, dsn); err != nil {
			db.Close()
			log.Errorf("LoadMarkers: database setup failed with %s", err)
			return err
		}
		defer db.Close()
		if err = migrateVectorMarkers(); err != nil {
			log.Errorf("LoadMarkers: migration of vector_markers failed with %s", err)
			return err
		}
		if err = dbms.Db().Exec("ALTER TABLE `vector_marker_faces` ADD VECTOR INDEX (embedding) M=8 DISTANCE=cosine").Error; err != nil {
			log.Errorf("LoadMarkers: vector_marker_faces index setup failed with %s", err)
			return err
		}
		// What does this do to the tables?
		if err = migrateVectorMarkers(); err != nil {
			log.Errorf("LoadMarkers: migration of vector_markers failed with %s", err)
			return err
		}

	case dbms.Postgres:
		if db, err = setupGormDB(driver, dsn); err != nil {
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
		if err = dbms.Db().Exec("CREATE INDEX ON vector_marker_faces USING hnsw (embedding vector_cosine_ops) WITH (m=8)").Error; err != nil {
			log.Errorf("LoadMarkers: vector_marker_faces index setup failed with %s", err)
			return err
		}
		// Does this drop the index?
		if err = migrateVectorMarkers(); err != nil {
			log.Errorf("LoadMarkers: migration of vector_markers failed with %s", err)
			return err
		}

	case dbms.SQLite3:
		if db, err = setupGormDB(driver, dsn); err != nil {
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

	}

	var csvFile *os.File
	if csvFile, err = os.Open(fileName); err != nil {
		log.Errorf("generateMarkers: unable to open required file %s with error %s", fileName, err)
		return err
	}
	defer func() {
		if err := csvFile.Close(); err != nil {
			log.Errorf("generateMarkers: unable to close file %s with error %s", fileName, err)
		}
	}()

	csvReader := csv.NewReader(csvFile)
	markers := make([]VectorMarker, 100)
	faceEmbeddings := make([]VectorMarkerFace, 100)
	counter := 0
	record := 1
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
			log.Errorf("generateMarkers: unable to read record %d from %s with error %s", record, fileName, err)
			return err
		}
		if !headerRead {
			headerRead = true
			continue
		}
		if markerReview, err = strconv.ParseBool(csvRecord[5]); err != nil {
			log.Errorf("generateMarkers: unable to to understand bool value from record %d value %s from %s with error %s", record, csvRecord[5], fileName, err)
			return err
		}
		if markerInvalid, err = strconv.ParseBool(csvRecord[6]); err != nil {
			log.Errorf("generateMarkers: unable to to understand bool value from record %d value %s from %s with error %s", record, csvRecord[6], fileName, err)
			return err
		}
		if faceDist, err = strconv.ParseFloat(csvRecord[10], 64); err != nil {
			log.Errorf("generateMarkers: unable to to understand float64 value from record %d value %s from %s with error %s", record, csvRecord[10], fileName, err)
			return err
		}
		if x64, err := strconv.ParseFloat(csvRecord[12], 32); err != nil {
			log.Errorf("generateMarkers: unable to to understand float32 value from record %d value %s from %s with error %s", record, csvRecord[12], fileName, err)
			return err
		} else {
			x = float32(x64)
		}
		if y64, err := strconv.ParseFloat(csvRecord[13], 32); err != nil {
			log.Errorf("generateMarkers: unable to to understand float32 value from record %d value %s from %s with error %s", record, csvRecord[13], fileName, err)
			return err
		} else {
			y = float32(y64)
		}
		if w64, err := strconv.ParseFloat(csvRecord[14], 32); err != nil {
			log.Errorf("generateMarkers: unable to to understand float32 value from record %d value %s from %s with error %s", record, csvRecord[14], fileName, err)
			return err
		} else {
			w = float32(w64)
		}
		if h64, err := strconv.ParseFloat(csvRecord[15], 32); err != nil {
			log.Errorf("generateMarkers: unable to to understand float32 value from record %d value %s from %s with error %s", record, csvRecord[15], fileName, err)
			return err
		} else {
			h = float32(h64)
		}
		if cAt, err := time.Parse(CSVTimestampFormat, csvRecord[21]); err != nil {
			log.Errorf("generateMarkers: unable to to understand timestamp value from record %d value %s from %s with error %s", record, csvRecord[21], fileName, err)
			return err
		} else {
			createdAt = cAt
		}
		if uAt, err := time.Parse(CSVTimestampFormat, csvRecord[22]); err != nil {
			log.Errorf("generateMarkers: unable to to understand timestamp value from record %d value %s from %s with error %s", record, csvRecord[22], fileName, err)
			return err
		} else {
			updatedAt = uAt
		}
		markers[counter] = VectorMarker{
			MarkerUID:     csvRecord[0],
			FileUID:       csvRecord[1],
			MarkerType:    csvRecord[2],
			MarkerSrc:     csvRecord[3],
			MarkerReview:  markerReview,
			MarkerInvalid: markerInvalid,
			SubjSrc:       csvRecord[8],
			FaceDist:      faceDist,
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
		if counter == 100 {
			log.Infof("processing %d with counter %d", record, counter)
			if err = createDBMSMarkers(driver, record, &markers, &faceEmbeddings, log); err != nil {
				return err
			}
			counter = 0
		}
	}

	if counter > 0 {
		markers = markers[:counter]
		return createDBMSMarkers(driver, record, &markers, &faceEmbeddings, log)
	}

	return nil
}

func QueryMarkers(driver, dsn string, log *logrus.Logger) (err error) {
	var db *dbms.DbConn

	if db, err = connectGormDB(driver, dsn); err != nil {
		db.Close()
		log.Errorf("LoadMarkers: database setup failed with %s", err)
		return err
	}
	defer db.Close()

	var mID string
	if m, err := gorm.G[VectorMarker](dbms.Db()).First(context.Background()); err != nil {
		log.Errorf("LoadMarkers: VectorMarker first failed with %s", err)
		return err
	} else {
		mID = m.MarkerUID
	}

	var e string
	if m, err := gorm.G[VectorMarkerFace](dbms.Db()).Where("marker_uid = ?", mID).First(context.Background()); err != nil {
		log.Errorf("LoadMarkers: VectorMarkerFace first failed with %s", err)
		return err
	} else {
		e = m.Embedding.Embed
	}

	var c int64

	if c, err = gorm.G[VectorMarkerFace](dbms.Db()).Where(DBEmbedQuery("embedding").Equals(0.0, e)).Count(context.Background(), "*"); err != nil {
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
	return
}

// createDBMSMarkers uses Gorm to create the records in the database
func createDBMSMarkers(driver string, record int, markers *[]VectorMarker, faceEmbeddings *[]VectorMarkerFace, log *logrus.Logger) (err error) {
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

// MariaDBEmbedding encodes an Embedding in the native MariaDB format (set of IEEE 754 floating point numbers)
func MariaDBEmbedding(values Embedding) (result []byte) {
	result = make([]byte, len(values)*4)
	for i, value := range values {
		words := math.Float32bits(float32(value))
		binary.LittleEndian.PutUint32(result[i*4:], words)
	}
	return
}
