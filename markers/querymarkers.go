package markers

import (
	"context"
	"fmt"
	"time"

	"github.com/keif888/vector/dbms"
	"github.com/keif888/vector/pkg/dsn"
	"github.com/qdrant/go-client/qdrant"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// QueryMarkers demonstaights some general queries
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
		var score float32
		score = float32(0.64)
		limit := uint64(15)
		// Return up to 10 results, with full data
		if result, err := dbms.QClient().Query(context.Background(), &qdrant.QueryPoints{
			CollectionName: VectorMarker{}.TableName(),
			Query:          qdrant.NewQueryDense(e), // 4 results
			// Query: qdrant.NewQueryNearest(qdrant.NewVectorInputDense(e)), // 4 results
			// Query:          qdrant.NewQueryID(qdrant.NewIDNum(336)), // 3 results (missing 336)
			Limit:          &limit,
			ScoreThreshold: &score,
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
				},
			},
			WithPayload: qdrant.NewWithPayload(true),
			WithVectors: qdrant.NewWithVectors(true),
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
