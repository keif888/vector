package markers

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/keif888/vector/dbms"
	"github.com/keif888/vector/pkg/dsn"
	"github.com/qdrant/go-client/qdrant"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

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
				if len(distResults) == 0 {
					log.Infof("No matches found for %s", markerUID)
				}
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
