package markers

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/dustin/go-humanize/english"
	"github.com/keif888/vector/dbms"
	"github.com/keif888/vector/pkg/dsn"
	"github.com/keif888/vector/pkg/vector/alg"
	"github.com/klauspost/cpuid/v2"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func ClusterNew(dataSourceName dsn.DSN, equation int, bruteForce bool, log *logrus.Logger) (err error) {
	start := time.Now()
	if bruteForce {
		// 9s for 5k from MariaDB
		// 15s to get source for 25k records from MariaDB
		// 4m13.650403896s to cluster 25k records with 834 new clusters found, with high CPU entire time.
		var db *dbms.DbConn
		switch dataSourceName.Driver {
		case dsn.DriverSQLite3, dsn.DriverMySQL, dsn.DriverPostgres:

			if db, err = connectGormDB(dataSourceName.Driver, dataSourceName.ToString()); err != nil {
				db.Close()
				log.Errorf("ClusterNew: database setup failed with %s", err)
				return err
			}
			defer db.Close()
		default:
			return fmt.Errorf("driver %s is not supported for current mode", dataSourceName.Driver)
		}
		// Fetch unclustered face embeddings.
		embeddings, err := QueryEmbeddings(false, true, ClusterSizeThreshold, ClusterScoreThreshold)
		if err != nil {
			log.Errorf("ClusterNew: QueryEmbeddings failed with %s", err)
			return err
		}
		log.Debugf("ClusterNew: found %d unclustered faces", len(embeddings))
		var c alg.HardClusterer

		// See https://dl.photoprism.app/research/ for research on face clustering algorithms.
		if c, err = alg.DBSCANWithProgress(ClusterCore, ClusterDist, IndexWorkers(), alg.EuclideanDist, 15*time.Minute, func(done, total int) {
			log.Infof("cluster: processing %d of %d", done, total)
		}); err != nil {
			return err
		} else if err = c.Learn(embeddings.Float64()); err != nil {
			return err
		}

		sizes := c.Sizes()

		if len(sizes) > 0 {
			log.Infof("ClusterNew: found %s", english.Plural(len(sizes), "new cluster", "new clusters"))
		} else {
			log.Debugf("ClusterNew: found no new clusters")
		}

		results := make([]Embeddings, len(sizes))

		for i := range sizes {
			results[i] = make(Embeddings, 0, sizes[i])
		}

		guesses := c.Guesses()

		for i, n := range guesses {
			if n < 1 {
				continue
			}

			results[n-1] = append(results[n-1], embeddings[i])
		}

		// Skipping the face creation process.
		/*
			start := time.Now()
			resultLen := len(results)

			for i, cluster := range results {
				if time.Since(start) > time.Duration(time.Minute*15) {
					log.Infof("cluster: added %d of %d faces", i, resultLen)
					start = time.Now()
				}
				if f := entity.NewFace("", entity.SrcAuto, cluster); f == nil {
					log.Errorf("faces: face must not be nil - you may have found a bug")
				} else if f.SkipMatching() {
					log.Infof("faces: skipped cluster %s, embedding not distinct enough", f.ID)
				} else if err = f.Create(); err == nil {
					added = append(added, *f)
					log.Debugf("faces: added cluster %s based on %s, radius %f", f.ID, english.Plural(f.Samples, "sample", "samples"), f.SampleRadius)
				} else if err = f.Updates(entity.Values{"updated_at": entity.Now()}); err != nil {
					log.Errorf("faces: %s", err)
				} else {
					log.Debugf("faces: updated cluster %s", f.ID)
				}
			}
		*/

	} else {
		if dataSourceName.Driver == dsn.DriverQdrant {
			// ToDo: implement cluster search with Qdrant
		} else {
			clusterCount := 0
			dbTime := time.Second * 0
			var startdb time.Time
			var db *dbms.DbConn
			if db, err = connectGormDB(dataSourceName.Driver, dataSourceName.ToString()); err != nil {
				db.Close()
				log.Errorf("ClusterNew: database setup failed with %s", err)
				return err
			}
			defer db.Close()

			// Attempt 3 (MariaDB Only at this stage), try and improve the query, so there is less network overhead?
			// Removed the network overhead, but still 22s execution time for 5k rows.
			// 7m25s for 25k rows.  CPU < 25% overall, with peak at 50%.  Much lower than GO clustering.
			// 8m21s for 25k rows, updating MariaDB as each cluster was found.
			unclustered := map[string]string{}
			clusters := map[string]string{}
			vector := VectorMarkerFace{}
			var results *sql.Rows
			updateDB := true

			// startdb = time.Now()
			if results, err = dbms.Db().
				Model(&VectorMarkerFace{}).
				Joins("INNER JOIN vector_markers ON vector_markers.marker_uid = vector_marker_faces.marker_uid").
				Where("marker_type = ?", MarkerFace).
				Where("marker_invalid = FALSE").
				Where("embeddings_json <> ''").
				Where("size >= ?", ClusterSizeThreshold).
				Where("score >= ?", ClusterScoreThreshold).
				Where("face_id = ''").
				Where("clustered_at is null").
				Rows(); err != nil {
				log.Errorf("ClusterNew: Select failed with %s", err)
				return err
			}
			for results.Next() {
				if err = dbms.Db().ScanRows(results, &vector); err != nil {
					log.Errorf("ClusterNew: ScanRows failed with %s", err)
					return err
				}
				unclustered[vector.MarkerUID] = vector.Embedding.Embed
			}
			if err = results.Close(); err != nil {
				log.Errorf("ClusterNew: Close failed with %s", err)
				return err
			}
			// dbTime += time.Since(startdb)
			type DistResult struct {
				Distance  float64
				MarkerUID string
			}

			for len(unclustered) != 0 {
				currentMarker := ""
				for markerUID := range unclustered {
					currentMarker = markerUID
					break
				}
				faceEmbedding := unclustered[currentMarker]
				var distResults []DistResult
				startdb = time.Now()
				//selectStr := fmt.Sprintf("VEC_DISTANCE_EUCLIDEAN(embedding, (select embedding from vector_marker_faces where marker_uid = '%s')) as distance, vector_marker_faces.marker_uid", currentMarker)
				if result := dbms.Db().
					Model(&VectorMarker{}).
					Joins("INNER JOIN vector_marker_faces ON vector_markers.marker_uid = vector_marker_faces.marker_uid").
					Where("marker_type = ?", MarkerFace).
					Where("marker_invalid = FALSE").
					Where("size >= ?", ClusterSizeThreshold).
					Where("score >= ?", ClusterScoreThreshold).
					Where("face_id = ''").
					Where("clustered_at is null").
					Where("VEC_DISTANCE_EUCLIDEAN(embedding, (select embedding from vector_marker_faces where marker_uid = ?)) <= ?", currentMarker, ClusterDist).
					// Where(DBEmbedQuery("embedding").
					// 	LessThanOrEquals(DistanceEquation(equation), ClusterDist, faceEmbedding),
					// ).
					// Select("?, vector_marker_faces.marker_uid", DBEmbedQuery("embedding").Distance(DistanceEquation(equation), faceEmbedding, "distance")).
					Select("VEC_DISTANCE_EUCLIDEAN(embedding, (select embedding from vector_marker_faces where marker_uid = ?)) as distance, vector_marker_faces.marker_uid", currentMarker).
					Find(&distResults); result.Error != nil {
					log.Errorf("ClusterNew: Select failed with %s", result.Error)
					return result.Error
				} else {
					dbTime += time.Since(startdb)
					if len(distResults) >= ClusterCore {
						clusterCount++
						clusters[currentMarker] = faceEmbedding
					}
					markerUIDs := []string{}
					for _, r := range distResults {
						markerUIDs = append(markerUIDs, r.MarkerUID)
						delete(unclustered, r.MarkerUID)
					}
					if updateDB {
						startdb = time.Now()
						e, _ := UnmarshalEmbedding(faceEmbedding)
						ej, _ := json.Marshal(e)
						s := sha1.Sum(ej) //nolint:gosec // G401: Stable identifier hash; not used for security decisions.
						faceID := base32.StdEncoding.EncodeToString(s[:])
						if len(markerUIDs) == 1 {
							faceID = ""
						}
						cAt := time.Now()
						if _, err = gorm.G[VectorMarker](dbms.Db()).
							Where("marker_uid in (?)", markerUIDs).
							Updates(context.Background(), VectorMarker{ClusteredAt: &cAt, FaceID: faceID}); err != nil {
							log.Errorf("ClusterNew: update failed with err %s", err)
							return err
						}
						dbTime += time.Since(startdb)
					}
				}
			}
			/*
				// Attempt 2
				// Load into maps, and then process.
				// The following took ~21s to run against MariaDB, compared to ~9s for the Go cluster against a 5k record set with 183 clusters found.
				// So it is not acceptable.

				unclustered := map[string]string{}
				clusters := map[string]string{}
				vector := VectorMarkerFace{}
				var results *sql.Rows

				// startdb = time.Now()
				if results, err = dbms.Db().
					Model(&VectorMarkerFace{}).
					Joins("INNER JOIN vector_markers ON vector_markers.marker_uid = vector_marker_faces.marker_uid").
					Where("marker_type = ?", MarkerFace).
					Where("marker_invalid = FALSE").
					Where("embeddings_json <> ''").
					Where("size >= ?", ClusterSizeThreshold).
					Where("score >= ?", ClusterScoreThreshold).
					Where("face_id = ''").
					Where("clustered_at is null").
					Rows(); err != nil {
					log.Errorf("ClusterNew: Select failed with %s", err)
					return err
				}
				for results.Next() {
					if err = dbms.Db().ScanRows(results, &vector); err != nil {
						log.Errorf("ClusterNew: ScanRows failed with %s", err)
						return err
					}
					unclustered[vector.MarkerUID] = vector.Embedding.Embed
				}
				if err = results.Close(); err != nil {
					log.Errorf("ClusterNew: Close failed with %s", err)
					return err
				}
				// dbTime += time.Since(startdb)
				type DistResult struct {
					Distance  float64
					MarkerUID string
				}

				for len(unclustered) != 0 {
					currentMarker := ""
					for markerUID := range unclustered {
						currentMarker = markerUID
						break
					}
					faceEmbedding := unclustered[currentMarker]
					var distResults []DistResult
					startdb = time.Now()
					if result := dbms.Db().
						Model(&VectorMarker{}).
						Joins("INNER JOIN vector_marker_faces ON vector_markers.marker_uid = vector_marker_faces.marker_uid").
						Where("marker_type = ?", MarkerFace).
						Where("marker_invalid = FALSE").
						Where("size >= ?", ClusterSizeThreshold).
						Where("score >= ?", ClusterScoreThreshold).
						Where("face_id = ''").
						Where("clustered_at is null").
						Where(DBEmbedQuery("embedding").
							LessThanOrEquals(DistanceEquation(equation), ClusterDist, faceEmbedding),
						).
						Select("?, vector_marker_faces.marker_uid", DBEmbedQuery("embedding").Distance(DistanceEquation(equation), faceEmbedding, "distance")).
						Find(&distResults); result.Error != nil {
						log.Errorf("ClusterNew: Select failed with %s", result.Error)
						return result.Error
					} else {
						dbTime += time.Since(startdb)
						if len(distResults) >= ClusterCore {
							clusterCount++
							clusters[currentMarker] = faceEmbedding
						}
						markerUIDs := []string{}
						for _, r := range distResults {
							markerUIDs = append(markerUIDs, r.MarkerUID)
							delete(unclustered, r.MarkerUID)
						}
						// startdb = time.Now()
						// if _, err = gorm.G[VectorMarker](dbms.Db()).
						// 	Where("marker_uid in (?)", markerUIDs).
						// 	Update(context.Background(), "clustered_at", time.Now()); err != nil {
						// 	log.Errorf("ClusterNew: update failed with err %s", err)
						// 	return err
						// }
						// dbTime += time.Since(startdb)
					}

				}
			*/
			/*
				// Attempt 1
				// Everything in the database
				// The following took ~59s to run against MariaDB, compared to ~9s for the Go cluster against a 5k record set with 183 clusters found.
				// So it is not acceptable.
				// Just the distance query took 23s, so even that is not acceptable.
				for {
					matchMe := VectorMarkerFace{}
					startdb = time.Now()
					if result := dbms.Db().
						Model(&VectorMarkerFace{}).
						Joins("INNER JOIN vector_markers ON vector_markers.marker_uid = vector_marker_faces.marker_uid").
						Where("marker_type = ?", MarkerFace).
						Where("marker_invalid = FALSE").
						Where("embeddings_json <> ''").
						Where("size >= ?", ClusterSizeThreshold).
						Where("score >= ?", ClusterScoreThreshold).
						Where("face_id = ''").
						Where("clustered_at is null").
						First(&matchMe); result.Error != nil {
						if errors.Is(result.Error, gorm.ErrRecordNotFound) {
							break
						}
						log.Errorf("ClusterNew: Select failed with %s", result.Error)
						return result.Error
					}
					dbTime += time.Since(startdb)
					type DistResult struct {
						Distance  float64
						MarkerUID string
					}

					faceEmbedding := matchMe.Embedding.Embed
					var distResults []DistResult
					startdb = time.Now()
					if result := dbms.Db().
						Model(&VectorMarker{}).
						Joins("INNER JOIN vector_marker_faces ON vector_markers.marker_uid = vector_marker_faces.marker_uid").
						Where("marker_type = ?", MarkerFace).
						Where("marker_invalid = FALSE").
						Where("size >= ?", ClusterSizeThreshold).
						Where("score >= ?", ClusterScoreThreshold).
						Where("face_id = ''").
						Where("clustered_at is null").
						Where(DBEmbedQuery("embedding").
							LessThanOrEquals(DistanceEquation(equation), ClusterDist, faceEmbedding),
						).
						Select("?, vector_marker_faces.marker_uid", DBEmbedQuery("embedding").Distance(DistanceEquation(equation), faceEmbedding, "distance")).
						Find(&distResults); result.Error != nil {
						log.Errorf("ClusterNew: Select failed with %s", result.Error)
						return result.Error
					} else {
						dbTime += time.Since(startdb)
						if len(distResults) >= ClusterCore {
							clusterCount++
						}
						markerUIDs := []string{}
						for _, r := range distResults {
							markerUIDs = append(markerUIDs, r.MarkerUID)
						}
						startdb = time.Now()
						if _, err = gorm.G[VectorMarker](dbms.Db()).
							Where("marker_uid in (?)", markerUIDs).
							Update(context.Background(), "clustered_at", time.Now()); err != nil {
							log.Errorf("ClusterNew: update failed with err %s", err)
							return err
						}
						dbTime += time.Since(startdb)
					}
				}
			*/
			log.Infof("database time was %s", dbTime)
			if clusterCount > 0 {
				log.Infof("ClusterNew: found %s", english.Plural(clusterCount, "new cluster", "new clusters"))
			} else {
				log.Debugf("ClusterNew: found no new clusters")
			}
		}
	}
	/*
		// Fetch unclustered face embeddings.
		embeddings, err := query.Embeddings(false, true, face.ClusterSizeThreshold, face.ClusterScoreThreshold)
		log.Debugf("faces: found %d", len(embeddings))
			var c alg.HardClusterer

			// See https://dl.photoprism.app/research/ for research on face clustering algorithms.
			if c, err = alg.DBSCANWithProgress(face.ClusterCore, face.ClusterDist, w.conf.IndexWorkers(), alg.EuclideanDist, 15*time.Minute, func(done, total int) {
				log.Infof("cluster: processing %d of %d", done, total)
			}); err != nil {
				return added, err
			} else if err = c.Learn(embeddings.Float64()); err != nil {
				return added, err
			}

			sizes := c.Sizes()

			if len(sizes) > 0 {
				log.Infof("faces: found %s", english.Plural(len(sizes), "new cluster", "new clusters"))
			} else {
				log.Debugf("faces: found no new clusters")
			}

			// Skipping the face creation process.
	*/
	log.Infof("Cluster took %s", time.Since(start))
	return nil
}

// Embeddings returns existing face embeddings.
func QueryEmbeddings(single, unclustered bool, size, score int) (result Embeddings, err error) {
	var col []string

	stmt := dbms.Db().
		Model(VectorMarker{}).
		Joins("INNER JOIN vector_marker_faces ON vector_markers.marker_uid = vector_marker_faces.marker_uid").
		Where("marker_type = ?", MarkerFace).
		Where("marker_invalid = FALSE").
		Where("embeddings_json <> ''").
		Order("vector_markers.marker_uid")

	if size > 0 {
		stmt = stmt.Where("size >= ?", size)
	}

	if score > 0 {
		stmt = stmt.Where("score >= ?", score)
	}

	if unclustered {
		stmt = stmt.Where("face_id = ''")
	}

	if err := stmt.Pluck("embeddings_json", &col).Error; err != nil {
		return result, err
	}

	for _, embeddingsJson := range col {
		if embeddingsJson == "" {
			continue
		} else if embeddings, err := UnmarshalEmbeddings(embeddingsJson); err != nil {
			log.Warnf("faces: %s", err)
		} else if !embeddings.Empty() {
			if single {
				// Single embedding per face detected.
				result = append(result, embeddings[0])
			} else {
				// Return all embedding otherwise.
				result = append(result, embeddings...)
			}
		}
	}

	return result, nil
}

// IndexWorkers returns the number of indexing workers.
func IndexWorkers() int {

	// NumCPU returns the number of logical CPU cores.
	cores := min(
		// Limit to physical cores to avoid high load on HT capable CPUs.
		runtime.NumCPU(), cpuid.CPU.PhysicalCores)

	// Use half the available cores by default.
	if cores > 1 {
		return cores / 2
	}

	return 1
}

// UnmarshalEmbedding parses a single face embedding JSON.
func UnmarshalEmbedding(s string) (result Embedding, err error) {
	if s == "" {
		return result, fmt.Errorf("cannot unmarshal embedding, empty string provided")
	} else if !strings.HasPrefix(s, "[") {
		return result, fmt.Errorf("cannot unmarshal embedding, invalid json provided")
	}

	err = json.Unmarshal([]byte(s), &result)

	normalizeEmbedding(result)

	return result, err
}

// UnmarshalEmbeddings parses face embedding JSON.
func UnmarshalEmbeddings(s string) (result Embeddings, err error) {
	if s == "" {
		return result, fmt.Errorf("cannot unmarshal empeddings, empty string provided")
	} else if !strings.HasPrefix(s, "[[") {
		return result, fmt.Errorf("cannot unmarshal empeddings, invalid json provided")
	}

	err = json.Unmarshal([]byte(s), &result)

	for i := range result {
		normalizeEmbedding(result[i])
	}

	return result, err
}

// Float64 returns embeddings as a float64 slice.
func (embeddings Embeddings) Float64() [][]float64 {
	result := make([][]float64, len(embeddings))

	for i, e := range embeddings {
		result[i] = e
	}

	return result
}
