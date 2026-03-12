package dbms

import (
	"sync"

	"github.com/keif888/vector/pkg/dsn"
	"github.com/qdrant/go-client/qdrant"
)

// qConn is the global qdrant.Client connection provider.
var qConn Qdrants

// Qdrant is a qdrant.Client connection provider interface.
type Qdrants interface {
	QClient() *qdrant.Client
}

// QConn is a Qdrant connection provider.
type QConn struct {
	Dsn dsn.DSN

	once   sync.Once
	client *qdrant.Client
}

// QClient returns the default *qdrant.Client connection.
func QClient() *qdrant.Client {
	if qConn == nil {
		return nil
	}

	return qConn.QClient()
}

// QClient returns the Qdrant client connection.
func (g *QConn) QClient() *qdrant.Client {
	g.once.Do(g.Open)

	if g.client == nil {
		log.Fatal("migrate: database not connected")
	}

	return g.client
}

// Close closes the gorm db connection.
func (g *QConn) Close() {
	if g.client != nil {
		if err := g.client.Close(); err != nil {
			log.Fatal(err)
		}
		g.client = nil
	}
}

// SetClientProvider sets the Qdrant connection provider.
func SetClientProvider(conn Qdrants) {
	qConn = conn
}

// HasClientProvider returns true if a db provider exists.
func HasClientProvider() bool {
	return dbConn != nil
}

// Open creates a new gorm db connection.
func (g *QConn) Open() {
	log.Info("Opening Qdrant connection")
	var client *qdrant.Client
	var err error
	if client, err = qdrant.NewClient(&qdrant.Config{
		Host:   g.Dsn.Host(),
		Port:   g.Dsn.Port(),
		APIKey: g.Dsn.Password,
		//ToDo: handle the other parameters here.
	}); err != nil {
		log.Errorf("setupQdrantDB: connection failed with %s", err)
		return
	}

	log.Info("DB connection established successfully")

	g.client = client
}
