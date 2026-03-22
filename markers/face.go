package markers

import (
	"crypto/sha1"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/keif888/vector/dbms"
)

// NullEmbedding is a zero-value placeholder embedding used when no data is available.
var NullEmbedding = make(Embedding, 512)

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

// NewFace returns a new face.
func NewFace(subjUID, faceSrc string, embeddings Embeddings) *Face {
	result := &Face{
		SubjUID: subjUID,
		FaceSrc: faceSrc,
	}

	if err := result.SetEmbeddings(embeddings); err != nil {
		log.Errorf("face: failed setting embeddings (%s)", err)
	}

	return result
}

// SetEmbeddings assigns face embeddings.
func (m *Face) SetEmbeddings(embeddings Embeddings) (err error) {
	if len(embeddings) == 0 {
		return fmt.Errorf("invalid embedding")
	}

	m.embedding, m.SampleRadius, m.Samples = EmbeddingsMidpoint(embeddings)

	if len(m.embedding) != len(NullEmbedding) {
		return fmt.Errorf("embedding has invalid number of values")
	}

	// Limit sample radius to reduce false positives.
	if m.SampleRadius > ClusterRadius {
		m.SampleRadius = ClusterRadius
	}

	m.EmbeddingJSON, err = json.Marshal(m.embedding)

	if err != nil {
		return err
	}

	//nolint:gosec // G401: Stable identifier hash; not used for security decisions.
	s := sha1.Sum(m.EmbeddingJSON)

	// Update Face ID, Kind, and reset match timestamp,
	m.ID = base32.StdEncoding.EncodeToString(s[:])

	if k := int(m.embedding.Kind()); k > m.FaceKind {
		m.FaceKind = k
	}

	m.MatchedAt = nil

	return nil
}

// EmbeddingsMidpoint returns the embeddings vector midpoint.
func EmbeddingsMidpoint(embeddings Embeddings) (result Embedding, radius float64, count int) {
	// Return if there are no embeddings.
	if embeddings.Empty() {
		return Embedding{}, 0, 0
	}

	// Count embeddings.
	count = len(embeddings)

	var first Embedding
	for _, emb := range embeddings {
		first = emb
		break
	}

	// Only one embedding?
	if count == 1 {
		// Return embedding if there is only one.
		return first, 0.0, 1
	}

	dim := len(first)

	// No embedding values?
	if dim == 0 {
		return Embedding{}, 0.0, count
	}

	result = make(Embedding, dim)

	invCount := 1.0 / float64(count)

	for i := range embeddings {
		emb := embeddings[i]

		if len(emb) != dim {
			continue
		}

		normalizeEmbedding(emb)

		for j := range dim {
			result[j] += emb[j]
		}
	}

	for i := range dim {
		result[i] *= invCount
	}

	normalizeEmbedding(result)

	// Radius is the max embedding distance + 0.01 from result.
	for _, emb := range embeddings {
		var dist float64

		for i := range dim {
			diff := result[i] - emb[i]
			dist += diff * diff
		}

		if d := math.Sqrt(dist); d > radius {
			radius = d + 0.01
		}
	}

	return result, radius, count
}

// Kind returns the type of face e.g. regular, children, or background.
func (m Embedding) Kind() Kind {

	return RegularFace
}

// Kind identifies the type of embedding.
type Kind int

const (
	// RegularFace represents a standard face embedding.
	RegularFace Kind = iota + 1
	// ChildrenFace represents a child face embedding.
	ChildrenFace
	// BackgroundFace represents non-face/background embeddings.
	BackgroundFace
	// AmbiguousFace represents embeddings that should be treated as uncertain.
	AmbiguousFace
)

// SkipMatching checks if the face embedding seems unsuitable for matching.
func (m Embedding) SkipMatching() bool {
	return false
}

// Create inserts the face to the database.
func (m *Face) Create() error {
	if m.ID == "" {
		return fmt.Errorf("empty id")
	}

	return dbms.Db().Create(m).Error
}

// Updates face properties in the database.
func (m *Face) Updates(values any) error {
	if m.ID == "" {
		return fmt.Errorf("empty id")
	}

	// UpdateFaces.Store(true)

	return dbms.Db().Model(m).Updates(values).Error
}
