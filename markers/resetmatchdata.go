package markers

import (
	"context"
	"time"

	"github.com/keif888/vector/dbms"
	"github.com/keif888/vector/pkg/dsn"
	"github.com/qdrant/go-client/qdrant"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// ResetMatchData retreives the saved markers from fileName and loads them into the table
func ResetMatchData(dataSourceName dsn.DSN, log *logrus.Logger) (err error) {
	start := time.Now()
	switch dataSourceName.Driver {
	case dsn.DriverMySQL, dsn.DriverPostgres, dsn.DriverSQLite3:
		var db *dbms.DbConn

		if db, err = connectGormDB(dataSourceName.Driver, dataSourceName.ToString()); err != nil {
			db.Close()
			log.Errorf("ResetMatchData: database setup failed with %s", err)
			return err
		}
		defer db.Close()

		//updateMap := map[string]any{"clustered": false, "face_id": ""}

		if results, err := gorm.G[any](dbms.Db()).Table(VectorMarker{}.TableName()).Where("clustered = true").Updates(context.Background(), map[string]any{"clustered": false, "face_id": ""}); err != nil {
			log.Errorf("ResetMatchData: VectorMarkerFace first failed with %s", err)
			return err
		} else {
			log.Infof("resetmatchdata: reset %d records", results)
		}
	case dsn.DriverQdrant:
		connectQdrant(dataSourceName)
		defer func() {
			if err = dbms.QClient().Close(); err != nil {
				log.Errorf("QueryMarkers: Client Close failed with %s", err)
			}
		}()

		request := &qdrant.SetPayloadPoints{
			CollectionName: VectorMarker{}.TableName(),
			Payload:        qdrant.NewValueMap(map[string]any{"Clustered": false, "FaceID": ""}),
			PointsSelector: qdrant.NewPointsSelectorFilter(&qdrant.Filter{
				Must: []*qdrant.Condition{
					qdrant.NewMatchBool("Clustered", true),
				},
			}),
		}

		if _, err := dbms.QClient().SetPayload(context.Background(), request); err != nil {
			log.Errorf("ClusterNew: update failed with err %s", err)
			return err
		}
	}
	log.Infof("Reset took %s", time.Since(start))
	return
}
