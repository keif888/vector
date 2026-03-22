package markers

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"

	"encoding/json"

	"math"
	"math/big"

	"strconv"

	"time"

	"github.com/keif888/vector/dbms"
	"github.com/keif888/vector/pkg/dsn"
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
	Clustered      bool `sql:"index" json:"Clustered" yaml:"Clustered,omitempty"`
}

// TableName returns the entity table name.
func (VectorMarker) TableName() string {
	return "vector_markers"
}

// Embedding represents a face embedding.
type Embedding []float64

// Embedding32 represents a face embedding in float32.
type Embedding32 []float32

// Embeddings represents a face embedding cluster.
type Embeddings []Embedding

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
