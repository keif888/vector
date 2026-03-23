package markers

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/base32"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dustin/go-humanize/english"
	"github.com/keif888/vector/dbms"
	"github.com/keif888/vector/pkg/dsn"
	"github.com/keif888/vector/pkg/vector/alg"
	"github.com/qdrant/go-client/qdrant"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

func ClusterNewV2(dataSourceName dsn.DSN, equation int, bruteForce bool, log *logrus.Logger) (err error) {
	start := time.Now()
	postgresLimit := 15
	qdrantLimit := 15
	elapsed := time.Now()
	if bruteForce {
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

		if err = dbms.Db().AutoMigrate(&Face{}); err != nil {
			log.Errorf("ClusterNew: AutoMigrate Face failed with %s", err)
			return err
		}

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

		var added Faces
		if err = dbms.Db().Model(Face{}).Where("ID in (?)", alg.DBScanFaces(c)).Find(&added).Error; err != nil {
			log.Errorf("ClusterNew: select added faces failed with %s", err)
			return err
		}

		if n := len(added); n > 0 {
			log.Infof("faces: added %d new faces [%s]", n, time.Since(start))
		} else {
			log.Debugf("faces: found no new faces [%s]", time.Since(start))
		}

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
				limit := uint64(qdrantLimit) // down from 2000000
				matchdb = time.Now()
				matches := make(qdrantResults, 0)
				if err := matches.QdrantQueryMatches(faceEmbedding, limit, score); err != nil {
					log.Errorf("ClusterNewV2: Query for matches failed with %s", err)
					return err
				} else {
					matchTime += time.Since(matchdb)
					matchCount++
					delete(unclustered, currentMarker)
					pointIDs := []*qdrant.PointId{}

					ej, _ := json.Marshal(faceEmbedding)
					if len(matches) >= ClusterCore {
						clusterCount++
						clusters[currentMarker] = string(ej)
					}

					// Check if there are any other clusters that are near enough to this one
					moreMatches := true
					startPos := 0
					for moreMatches {
						startLen := len(matches)

						for i := startPos; i < startLen; i++ {
							if _, ok := unclustered[matches[i].MarkerUID]; ok {
								matchdb = time.Now()
								newMatch := matches
								if err := newMatch.QdrantQueryMatches(unclustered[matches[i].MarkerUID], limit, score); err != nil {
									log.Errorf("ClusterNewV2: Query for matches failed with %s", err)
									return err
								}
								matchTime += time.Since(matchdb)
								matchCount++
								if len(newMatch) >= ClusterCore+len(matches) {
									matches = append(matches, newMatch[len(matches):]...)
								}
								delete(unclustered, matches[i].MarkerUID)
							}
						}

						if len(matches) == startLen {
							moreMatches = false
						} else {
							startPos = startLen
						}
					}
					for _, match := range matches {
						markerFound := match.MarkerUID
						pointIDs = append(pointIDs, match.PointID)
						delete(unclustered, markerFound)
					}

					if len(matches) == 0 {
						log.Debugf("clusternewv2: no records found for %s", currentMarker)
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
				matches := make(dbmsResults, 0)
				matchdb = time.Now()
				if err := matches.DBMSQueryMatches(dataSourceName, currentMarker, postgresLimit); err != nil {
					log.Errorf("ClusterNewV2: Select failed with %s", err)
					return err
				} else {
					matchTime += time.Since(matchdb)
					matchCount++
					delete(unclustered, currentMarker)

					if len(matches) >= ClusterCore {
						clusterCount++
						clusters[currentMarker] = faceEmbedding
					}

					// Check if there are any other clusters that are near enough to this one
					moreMatches := true
					startPos := 0
					for moreMatches {
						startLen := len(matches)
						for i := startPos; i < startLen; i++ {
							if _, ok := unclustered[matches[i].MarkerUID]; ok {
								matchdb = time.Now()
								newMatch := matches
								if err := newMatch.DBMSQueryMatches(dataSourceName, matches[i].MarkerUID, postgresLimit); err != nil {
									log.Errorf("ClusterNewV2: Query for matches failed with %s", err)
									return err
								}
								matchTime += time.Since(matchdb)
								matchCount++
								if len(newMatch) >= ClusterCore+len(matches) {
									matches = append(matches, newMatch[len(matches):]...)
								}
								delete(unclustered, matches[i].MarkerUID)
							}
						}

						if len(matches) == startLen {
							moreMatches = false
						} else {
							startPos = startLen
						}
					}

					markerUIDs := []string{}
					for _, r := range matches {
						markerUIDs = append(markerUIDs, r.MarkerUID)
						delete(unclustered, r.MarkerUID)
					}

					if len(matches) == 0 {
						log.Debugf("clusternewv2: no records found for %s", currentMarker)
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
			log.Infof("match time was %s for %d matches, update time was %s", matchTime, matchCount, dbTime)
			if clusterCount > 0 {
				log.Infof("ClusterNew: found %s", english.Plural(clusterCount, "new cluster", "new clusters"))
			} else {
				log.Debugf("ClusterNew: found no new clusters")
			}
		}
	}
	log.Infof("Cluster took %s", time.Since(start))
	return nil
}

type qdrantResult struct {
	MarkerUID string
	PointID   *qdrant.PointId
}

type qdrantResults []qdrantResult

// QdrantQueryMatches attempts to return all the MarkerUID's and PointID's that are found when querying for faceEmbedding
func (matches *qdrantResults) QdrantQueryMatches(faceEmbedding []float32, limit uint64, score float32) (err error) {
	if matches == nil {
		return fmt.Errorf("QdrantQueryMatches: matches must not be nil")
	}
	allRead := false
	for !allRead {
		filter := &qdrant.Filter{
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
		}
		if len(*matches) > 0 {
			var p []*qdrant.PointId
			p = make([]*qdrant.PointId, 0)
			for _, r := range *matches {
				p = append(p, r.PointID)
			}
			filter.MustNot = []*qdrant.Condition{
				qdrant.NewHasID(p...),
			}
		}

		if results, err := dbms.QClient().Query(context.Background(), &qdrant.QueryPoints{
			CollectionName: VectorMarker{}.TableName(),
			Query:          qdrant.NewQueryDense(faceEmbedding),
			Filter:         filter,
			Limit:          &limit,
			ScoreThreshold: &score,
			WithPayload:    qdrant.NewWithPayload(true),
			WithVectors:    qdrant.NewWithVectors(false),
		}); err != nil {
			log.Errorf("QdrantQueryMatches: Query for matches failed with %s", err)
			return err
		} else {
			// log.Debugf("%+v", results)
			for _, result := range results {
				found := qdrantResult{MarkerUID: result.Payload["UID"].GetStringValue(), PointID: result.Id}
				*matches = append(*matches, found)
			}
			if len(results) != int(limit) {
				allRead = true
			}
		}
	}
	return nil
}

type DistResult struct {
	Distance  float64
	MarkerUID string
}

type dbmsResults []DistResult

func (matches *dbmsResults) DBMSQueryMatches(dataSourceName dsn.DSN, currentMarker string, limit int) (err error) {
	if matches == nil {
		return fmt.Errorf("DBMSQueryMatches: matches must not be nil")
	}

	allRead := false
	var results dbmsResults
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
		whereStmt = fmt.Sprintf("embedding match (select embedding from vector_marker_faces where marker_uid = ?) AND k = %d AND distance <= ?", limit)
	default:
		// How did we get here?
		return fmt.Errorf("ClusterNew: dsn driver %s not recognised", dataSourceName.Driver)
	}
	for !allRead {

		var query *gorm.DB

		switch dataSourceName.Driver {
		case dsn.DriverPostgres:
			if r := dbms.Db().Exec("SET hnsw.ef_search = 120;SET hnsw.iterative_scan = strict_order;"); r.Error != nil { //SET hnsw.ef_search = 120;
				log.Errorf("ClusterNew: SETs failed with %s", r.Error)
				return r.Error

			}
			if len(*matches) > 0 {
				matchedMarkers := make([]string, 0)
				for _, match := range *matches {
					matchedMarkers = append(matchedMarkers, match.MarkerUID)
				}
				// CTE as per pgvector readme.
				query = dbms.Db().
					Raw("WITH face_match AS MATERIALIZED ("+
						"SELECT embedding <-> (select embedding from vector_marker_faces where marker_uid = ?) as distance, vector_marker_faces.marker_uid "+
						"FROM vector_markers INNER JOIN vector_marker_faces ON vector_markers.marker_uid = vector_marker_faces.marker_uid "+
						"WHERE marker_type = ? AND marker_invalid = FALSE AND size >= ? AND score >= ? "+
						"AND vector_markers.marker_uid NOT IN (?)"+
						"AND face_id = '' AND clustered = false ORDER BY distance LIMIT ?) "+
						"SELECT distance, marker_uid FROM face_match "+
						"WHERE distance <= ? ORDER BY distance", currentMarker, MarkerFace, ClusterSizeThreshold, ClusterScoreThreshold, matchedMarkers, limit, ClusterDist)
			} else {
				// CTE as per pgvector readme.
				query = dbms.Db().
					Raw("WITH face_match AS MATERIALIZED ("+
						"SELECT embedding <-> (select embedding from vector_marker_faces where marker_uid = ?) as distance, vector_marker_faces.marker_uid "+
						"FROM vector_markers INNER JOIN vector_marker_faces ON vector_markers.marker_uid = vector_marker_faces.marker_uid "+
						"WHERE marker_type = ? AND marker_invalid = FALSE AND size >= ? AND score >= ? "+
						"AND face_id = '' AND clustered = false ORDER BY distance LIMIT ?) "+
						"SELECT distance, marker_uid FROM face_match "+
						"WHERE distance <= ? ORDER BY distance", currentMarker, MarkerFace, ClusterSizeThreshold, ClusterScoreThreshold, limit, ClusterDist)
			}
		case dsn.DriverMySQL:
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
			if len(*matches) > 0 {
				matchedMarkers := make([]string, 0)
				for _, match := range *matches {
					matchedMarkers = append(matchedMarkers, match.MarkerUID)
				}
				query.Where("vector_markers.marker_uid NOT IN (?)", matchedMarkers)
			}
		case dsn.DriverSQLite3:
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
			if len(*matches) > 0 {
				matchedMarkers := make([]string, 0)
				for _, match := range *matches {
					matchedMarkers = append(matchedMarkers, match.MarkerUID)
				}
				query.Joins("LEFT JOIN (SELECT marker_uid from vector_markers WHERE marker_UID in (?)) as Exclusions ON vector_markers.marker_uid = Exclusions.marker_uid", matchedMarkers)
				query.Where("Exclusions.marker_uid IS NULL")
			}

		}

		if result := query.
			Find(&results); result.Error != nil {
			log.Errorf("ClusterNew: Select failed with %s", result.Error)
			return result.Error
		} else {
			*matches = append(*matches, results...)
			if len(results) != int(limit) {
				allRead = true
			}
		}
	}
	return nil
}
