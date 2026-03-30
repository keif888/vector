package markers

import (
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
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
	var clustersFile *os.File
	if csvFile, err = os.Create(fileName); err != nil {
		log.Errorf("generateMarkers: unable to create required file %s with error %s", fileName, err)
		return err
	}
	defer func() {
		if err := csvFile.Close(); err != nil {
			log.Errorf("generateMarkers: unable to close file %s with error %s", fileName, err)
		}
	}()

	var csvWriter *csv.Writer
	var gzipWriter *gzip.Writer
	var clustersFileName string

	if filepath.Ext(fileName) == ".gz" {
		gzipWriter = gzip.NewWriter(csvFile)
		csvWriter = csv.NewWriter(gzipWriter)
		defer func() {
			csvWriter.Flush()
			if err := csvWriter.Error(); err != nil {
				log.Errorf("generateMarkers: unable to flush file %s with error %s", fileName, err)
			}
			if err := gzipWriter.Close(); err != nil {
				log.Errorf("generateMarkers: unable to flush file %s with error %s", fileName, err)
			}
		}()
		ts := strings.TrimSuffix(fileName, filepath.Ext(fileName))
		clustersFileName = strings.TrimSuffix(ts, filepath.Ext(ts)) + ".clusters.txt"
	} else {
		csvWriter = csv.NewWriter(csvFile)
		defer func() {
			csvWriter.Flush()
			if err := csvWriter.Error(); err != nil {
				log.Errorf("generateMarkers: unable to flush file %s with error %s", fileName, err)
			}
		}()
		clustersFileName = strings.TrimSuffix(fileName, filepath.Ext(fileName)) + ".clusters.txt"
	}

	if clustersFile, err = os.Create(clustersFileName); err != nil {
		log.Errorf("generateMarkers: unable to create required file %s with error %s", clustersFileName, err)
		return err
	}
	defer func() {
		if err := clustersFile.Close(); err != nil {
			log.Errorf("generateMarkers: unable to close file %s with error %s", clustersFileName, err)
		}
	}()

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

	clusters := map[int]string{}

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

		if marker.Size >= ClusterSizeThreshold && marker.Score >= ClusterScoreThreshold {
			if s, ok := clusters[faceNumber]; ok {
				var b strings.Builder
				b.WriteString(s)
				b.WriteString(",")
				b.WriteString(markerUID)
				clusters[faceNumber] = b.String()
			} else {
				clusters[faceNumber] = markerUID
			}
		}
	}

	clusterCount := 0
	for i := range numberOfFaces {
		if result, ok := clusters[i]; ok {
			mc := strings.Count(result, ",") + 1
			if mc >= ClusterCore {
				clusterCount++
			}
			ss := strings.Split(result, ",")
			slices.Sort(ss)

			if _, err := clustersFile.WriteString(strings.Join(ss, ",")); err != nil {
				log.Errorf("generateMarkers: unable to write record %d to file %s with error %s", i, clustersFileName, err)
				return err
			}
			if _, err := clustersFile.WriteString("\n"); err != nil {
				log.Errorf("generateMarkers: unable to write newline record %d to file %s with error %s", i, clustersFileName, err)
				return err
			}
		}
	}

	log.Infof("generateMarkers: wrote %d markers to %s with %d clusters", numberOfMarkers, fileName, clusterCount)
	return nil
}
