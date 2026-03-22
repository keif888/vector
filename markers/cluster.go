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
	"github.com/qdrant/go-client/qdrant"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func ClusterNew(dataSourceName dsn.DSN, equation int, bruteForce bool, log *logrus.Logger) (err error) {
	start := time.Now()
	postgresLimit := 15
	qdrantLimit := 15
	elapsed := time.Now()
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

		dbms.Db().AutoMigrate(&Face{})

		// Fetch unclustered face embeddings.
		embeddings, err := QueryEmbeddings(false, true, ClusterSizeThreshold, ClusterScoreThreshold)
		if err != nil {
			log.Errorf("ClusterNew: QueryEmbeddings failed with %s", err)
			return err
		}
		log.Debugf("ClusterNew: found %d unclustered faces", len(embeddings))
		var c alg.HardClusterer

		savef := SaveLearnResult

		// See https://dl.photoprism.app/research/ for research on face clustering algorithms.
		if c, err = alg.DBSCANWithProgress(ClusterCore, ClusterDist, IndexWorkers(), alg.EuclideanDist, 15*time.Minute, func(done, total int) {
			log.Infof("cluster: processing %d of %d", done, total)
		}, savef); err != nil {
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

		log.Infof("ClusterNew: found the following faces %+v", alg.DBScanFaces(c))
		var added Faces
		if err = dbms.Db().Model(Face{}).Where("ID in (?)", alg.DBScanFaces(c)).Find(added).Error; err != nil {
			log.Errorf("ClusterNew: select added faces failed with %s", err)
			return err
		}

		if n := len(added); n > 0 {
			log.Infof("faces: added %d new faces [%s]", n, time.Since(start))
		} else {
			log.Debugf("faces: found no new faces [%s]", time.Since(start))
		}
		// In theory at this point we have the added faces in added.
		// And the func (w *Faces) Cluster(opt FacesOptions) (added entity.Faces, err error) has been replicated
		// with on the fly save capability.

		// Just need to call the next steps to save everything off.

		/*
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

			for _, n := range guesses {
				if n < 1 {
					continue
				}

				log.Infof("len results[%d] = %d", n-1, len(results[n-1]))
			}
		*/
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
			clusterCount := 0
			dbTime := time.Second * 0
			matchTime := time.Second * 0
			matchCount := 0
			var startdb time.Time
			var matchdb time.Time

			connectQdrant(dataSourceName)
			defer func() {
				if err = dbms.QClient().Close(); err != nil {
					log.Errorf("QueryMarkers: Client Close failed with %s", err)
				}
			}()

			unclustered := map[string]Embedding32{}
			clusters := map[string]string{}
			updateDB := true
			offset := uint64(0)

			allRead := false
			for !allRead {
				if result, err := dbms.QClient().Scroll(context.Background(), &qdrant.ScrollPoints{
					CollectionName: VectorMarker{}.TableName(),
					Filter: &qdrant.Filter{
						Must: []*qdrant.Condition{
							qdrant.NewMatch("Type", MarkerFace),
							qdrant.NewMatchBool("Invalid", false),
							//				Where("embeddings_json <> ''").
							qdrant.NewRange("Size", &qdrant.Range{
								Gte: qdrant.PtrOf(float64(ClusterSizeThreshold)),
							}),
							qdrant.NewRange("Score", &qdrant.Range{
								Gte: qdrant.PtrOf(float64(ClusterScoreThreshold)),
							}),
							qdrant.NewMatch("FaceID", ""),
							qdrant.NewMatchBool("Clustered", false),
						},
					},
					Limit:       qdrant.PtrOf(uint32(1000)),
					Offset:      qdrant.NewIDNum(offset),
					WithPayload: qdrant.NewWithPayload(true),
					WithVectors: qdrant.NewWithVectors(true),
				}); err != nil {
					log.Errorf("ClusterNew: Scroll of offset=%d failed with %s", offset, err)
					return err
				} else {
					for _, r := range result {
						unclustered[r.Payload["UID"].GetStringValue()] = r.Vectors.GetVector().GetDense().Data
						if r.Id.GetNum() > offset {
							offset = r.Id.GetNum()
						}
					}
					if len(result) != 1000 {
						allRead = true
					}
				}
			}
			log.Infof("ClusterNew: found %d unclustered markers", len(unclustered))

			for len(unclustered) != 0 {
				if time.Since(elapsed) > time.Minute*15 {
					elapsed = time.Now()
					log.Infof("ClusterNew: %d remaining", len(unclustered))
				}
				currentMarker := ""
				for markerUID := range unclustered {
					currentMarker = markerUID
					break
				}
				faceEmbedding := unclustered[currentMarker]
				score := float32(ClusterDist)
				// ToDo: Add retry if 15 are found, like the Postgres code.
				limit := uint64(qdrantLimit) // down from 2000000
				matchdb = time.Now()
				if results, err := dbms.QClient().Query(context.Background(), &qdrant.QueryPoints{
					CollectionName: VectorMarker{}.TableName(),
					Query:          qdrant.NewQueryDense(faceEmbedding),
					Filter: &qdrant.Filter{
						Must: []*qdrant.Condition{
							qdrant.NewMatch("Type", MarkerFace),
							qdrant.NewMatchBool("Invalid", false),
							qdrant.NewRange("Size", &qdrant.Range{
								Gte: qdrant.PtrOf(float64(ClusterSizeThreshold)),
							}),
							qdrant.NewRange("Score", &qdrant.Range{
								Gte: qdrant.PtrOf(float64(ClusterScoreThreshold)),
							}),
							qdrant.NewMatch("FaceID", ""),
							qdrant.NewMatchBool("Clustered", false),
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
					matchTime += time.Since(matchdb)
					matchCount++
					pointIDs := []*qdrant.PointId{}

					ej, _ := json.Marshal(faceEmbedding)
					if len(results) >= ClusterCore {
						clusterCount++
						clusters[currentMarker] = string(ej)
					}

					for _, result := range results {
						markerFound := result.Payload["UID"].GetStringValue()
						pointIDs = append(pointIDs, result.Id)
						if markerFound != currentMarker || (len(results) != qdrantLimit && markerFound == currentMarker) {
							delete(unclustered, markerFound)
						} else {
							log.Debugf("clusternew: qdrant will retry %s", currentMarker)
						}
					}

					if len(results) == 0 {
						delete(unclustered, currentMarker)
						log.Debugf("clusternew: no records found for %s", currentMarker)
					} else {
						if updateDB {
							startdb = time.Now()
							if _, ok := clusters[currentMarker]; !ok && len(pointIDs) < ClusterCore {
								request := &qdrant.SetPayloadPoints{
									CollectionName: VectorMarker{}.TableName(),
									Payload: qdrant.NewValueMap(map[string]any{
										"Clustered": true,
									},
									),
									PointsSelector: qdrant.NewPointsSelector(pointIDs...),
								}

								if _, err = dbms.QClient().SetPayload(context.Background(), request); err != nil {
									log.Errorf("ClusterNew: update failed with err %s", err)
									return err
								}
							} else {

								s := sha1.Sum(ej) //nolint:gosec // G401: Stable identifier hash; not used for security decisions.
								faceID := base32.StdEncoding.EncodeToString(s[:])
								if len(pointIDs) == 1 {
									faceID = ""
								}

								request := &qdrant.SetPayloadPoints{
									CollectionName: VectorMarker{}.TableName(),
									Payload: qdrant.NewValueMap(map[string]any{
										"FaceID":    faceID,
										"Clustered": true,
									},
									),
									PointsSelector: qdrant.NewPointsSelector(pointIDs...),
								}

								if _, err = dbms.QClient().SetPayload(context.Background(), request); err != nil {
									log.Errorf("ClusterNew: update failed with err %s", err)
									return err
								}
							}
							dbTime += time.Since(startdb)
						}
					}
				}
			}
			log.Infof("match time was %s for %d matches, update time was %s", matchTime, matchCount, dbTime)
			if clusterCount > 0 {
				log.Infof("ClusterNew: found %s", english.Plural(clusterCount, "new cluster", "new clusters"))
			} else {
				log.Debugf("ClusterNew: found no new clusters")
			}

		} else {
			clusterCount := 0
			dbTime := time.Second * 0
			matchTime := time.Second * 0
			matchCount := 0
			var startdb time.Time
			var matchdb time.Time
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
				Where("clustered = false").
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
			log.Infof("ClusterNew: found %d unclustered markers", len(unclustered))

			for len(unclustered) != 0 {
				if time.Since(elapsed) > time.Minute*15 {
					elapsed = time.Now()
					log.Infof("ClusterNew: %d remaining", len(unclustered))
				}
				currentMarker := ""
				for markerUID := range unclustered {
					currentMarker = markerUID
					break
				}
				faceEmbedding := unclustered[currentMarker]
				var distResults []DistResult
				matchdb = time.Now()
				var selectStmt, whereStmt string
				switch dataSourceName.Driver {
				case dsn.DriverMySQL:
					selectStmt = "VEC_DISTANCE_EUCLIDEAN(embedding, (select embedding from vector_marker_faces where marker_uid = ?)) as distance, vector_marker_faces.marker_uid"
					whereStmt = "VEC_DISTANCE_EUCLIDEAN(embedding, (select embedding from vector_marker_faces where marker_uid = ?)) <= ?"
				case dsn.DriverPostgres:
					selectStmt = "embedding <-> (select embedding from vector_marker_faces where marker_uid = ?) as distance, vector_marker_faces.marker_uid"
					whereStmt = "embedding <-> (select embedding from vector_marker_faces where marker_uid = ?) <= ?"
				case dsn.DriverSQLite3:
					selectStmt = "vec_distance_L2(embedding, (select embedding from vector_marker_faces where marker_uid = ?)) as distance, vector_marker_faces.marker_uid"
					whereStmt = "embedding match (select embedding from vector_marker_faces where marker_uid = ?) AND k = 1024 AND distance <= ?"
				default:
					// How did we get here?
					return fmt.Errorf("ClusterNew: dsn driver %s not recognised", dataSourceName.Driver)
				}

				var query *gorm.DB

				if dataSourceName.Driver == dsn.DriverPostgres {
					if r := dbms.Db().Exec("SET hnsw.ef_search = 120;SET hnsw.iterative_scan = strict_order;"); r.Error != nil { //SET hnsw.ef_search = 120;
						log.Errorf("ClusterNew: SETs failed with %s", r.Error)
						return r.Error

					}
					// CTE as per pgvector readme.
					query = dbms.Db().
						Raw("WITH face_match AS MATERIALIZED ("+
							"SELECT embedding <-> (select embedding from vector_marker_faces where marker_uid = ?) as distance, vector_marker_faces.marker_uid "+
							"FROM vector_markers INNER JOIN vector_marker_faces ON vector_markers.marker_uid = vector_marker_faces.marker_uid "+
							"WHERE marker_type = ? AND marker_invalid = FALSE AND size >= ? AND score >= ? "+
							"AND face_id = '' AND clustered = false ORDER BY distance LIMIT ?) "+
							"SELECT distance, marker_uid FROM face_match "+
							"WHERE distance <= ? ORDER BY distance", currentMarker, MarkerFace, ClusterSizeThreshold, ClusterScoreThreshold, postgresLimit, ClusterDist)
				} else {
					query = dbms.Db().
						Model(&VectorMarker{}).
						Joins("INNER JOIN vector_marker_faces ON vector_markers.marker_uid = vector_marker_faces.marker_uid").
						Where("marker_type = ?", MarkerFace).
						Where("marker_invalid = FALSE").
						Where("size >= ?", ClusterSizeThreshold).
						Where("score >= ?", ClusterScoreThreshold).
						Where("face_id = ''").
						Where("clustered = false").
						Where(whereStmt, currentMarker, ClusterDist).
						Select(selectStmt, currentMarker)
				}
				if dataSourceName.Driver == dsn.DriverPostgreSQL {
					query.
						Order("distance").
						Limit(postgresLimit)
				}

				if result := query.
					Find(&distResults); result.Error != nil {
					log.Errorf("ClusterNew: Select failed with %s", result.Error)
					return result.Error
				} else {
					matchTime += time.Since(matchdb)
					matchCount++
					// return nil
					if len(distResults) >= ClusterCore {
						clusterCount++
						clusters[currentMarker] = faceEmbedding
					}
					markerUIDs := []string{}
					for _, r := range distResults {
						markerUIDs = append(markerUIDs, r.MarkerUID)
						if r.MarkerUID != currentMarker || dataSourceName.Driver != dsn.DriverPostgres || (dataSourceName.Driver == dsn.DriverPostgres && len(distResults) != postgresLimit && r.MarkerUID == currentMarker) {
							delete(unclustered, r.MarkerUID)
						} else {
							log.Debugf("clusternew: postgres will retry %s", currentMarker)
						}
					}
					// Just in case there is only postgresLimit records for a marker in Postgres.
					if len(distResults) == 0 {
						delete(unclustered, currentMarker)
						log.Debugf("clusternew: no records found for %s", currentMarker)
					} else {
						if updateDB {
							startdb = time.Now()
							if _, ok := clusters[currentMarker]; !ok && len(markerUIDs) < ClusterCore {
								if _, err = gorm.G[VectorMarker](dbms.Db()).
									Where("marker_uid in (?)", markerUIDs).
									Updates(context.Background(), VectorMarker{Clustered: true}); err != nil {
									log.Errorf("ClusterNew: update failed with err %s", err)
									return err
								}
							} else {
								e, _ := UnmarshalEmbedding(faceEmbedding)
								ej, _ := json.Marshal(e)
								s := sha1.Sum(ej) //nolint:gosec // G401: Stable identifier hash; not used for security decisions.
								faceID := base32.StdEncoding.EncodeToString(s[:])
								if _, err = gorm.G[VectorMarker](dbms.Db()).
									Where("marker_uid in (?)", markerUIDs).
									Updates(context.Background(), VectorMarker{Clustered: true, FaceID: faceID}); err != nil {
									log.Errorf("ClusterNew: update failed with err %s", err)
									return err
								}
							}
							dbTime += time.Since(startdb)
						}
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
					Where("clustered = false").
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
						Where("clustered = false").
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
						// 	Update(context.Background(), "clustered", true); err != nil {
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
						Where("clustered = false").
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
						Where("clustered = false").
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
							Update(context.Background(), "clustered", true); err != nil {
							log.Errorf("ClusterNew: update failed with err %s", err)
							return err
						}
						dbTime += time.Since(startdb)
					}
				}
			*/
			log.Infof("match time was %s for %d matches, update time was %s", matchTime, matchCount, dbTime)
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

// Float64ToEmbeddings converts a float64[][] to an Embeddings
func Float64ToEmbeddings(f [][]float64) (result Embeddings) {
	result = make(Embeddings, len(f))
	for i, e := range f {
		result[i] = e
	}
	return
}

// SaveLearnResult saves the face that was found.  But it doesn't allow a restart of clustering from the crash point.
// ToDo: Update the Clustered flag on the Marker.  The issue, how do we determine which marker we were playing with?
func SaveLearnResult(f [][]float64) (faceID string) {
	cluster := Float64ToEmbeddings(f)
	faceID = ""

	if f := NewFace("", SrcAuto, cluster); f == nil {
		log.Errorf("faces: face must not be nil - you may have found a bug")
	} else if f.SkipMatching() {
		log.Infof("faces: skipped cluster %s, embedding not distinct enough", f.ID)
	} else if err := f.Create(); err == nil {
		faceID = f.ID
		// added = append(added, *f)
		log.Debugf("faces: added cluster %s based on %s, radius %f", f.ID, english.Plural(f.Samples, "sample", "samples"), f.SampleRadius)
	} else if err = f.Updates(Values{"updated_at": time.Now()}); err != nil {
		log.Errorf("faces: %s", err)
	} else {
		log.Debugf("faces: updated cluster %s", f.ID)
	}
	return
}

// SkipMatching checks whether the face should be skipped when matching.
func (m *Face) SkipMatching() bool {
	return m.FaceKind > 1 || m.Embedding().SkipMatching()
}

// Values is a shorthand alias for map[string]interface{}.
type Values = map[string]any
