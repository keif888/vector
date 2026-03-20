package markers

import (
	"encoding/csv"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
)

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

	csvHeader := []string{"marker_uid", "file_uid", "marker_type", "marker_src", "marker_name", "marker_review", "marker_invalid", "subj_uid", "subj_src", "face_id", "face_dist", "embedding", "x", "y", "w", "h", "q", "size", "score", "thumb", "matched_at", "created_at", "updated_at", "clustered"}
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
			Clustered: false,
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
			strconv.FormatBool(marker.Clustered),
		}
		if err := csvWriter.Write(csvRecord); err != nil {
			log.Errorf("generateMarkers: unable to write record %d to file %s with error %s", i, fileName, err)
			return err
		}

	}
	log.Infof("generateMarkers: wrote %d markers to %s", numberOfMarkers, fileName)
	return nil
}
