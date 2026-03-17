package markers

import (
	"context"
	"database/sql/driver"
	"encoding/binary"
	"fmt"
	"math"
	"strings"

	"github.com/sirupsen/logrus"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"gorm.io/gorm/schema"
)

type DistanceEquation int

const (
	Distance_Cosine DistanceEquation = iota
	Distance_Euclidean
)

var log *logrus.Logger

func SetLog(logger *logrus.Logger) {
	log = logger
}

// DBEmbed defined type to enable driver.Valuer and sql.Scanner interfaces
type DBEmbed struct {
	Embed string
}

// value return embed value, implement driver.Valuer interface
func (e DBEmbed) Value() (driver.Value, error) {
	if len(e.Embed) == 0 {
		return nil, nil
	}
	return e, nil
}

// GormDBDataType returns gorm DB data type based on the current using database.
func (DBEmbed) GormDBDataType(db *gorm.DB, field *schema.Field) string {
	switch db.Name() {
	case "mysql":
		return fmt.Sprintf("VECTOR(%d) NOT NULL", field.Size)
	case "postgres":
		return fmt.Sprintf("VECTOR(%d)", field.Size)
	case "sqlite":
		// return fmt.Sprintf("FLOAT(%d)", field.Size)
		return "blob"
	default:
		return ""
	}
}

func (e *DBEmbed) Scan(value interface{}) error {
	var valueBytes []byte
	if s, ok := value.(fmt.Stringer); ok {
		e.Embed = s.String()
		return nil
	}
	switch v := value.(type) {
	case []byte:
		// Assumes that this is MariaDB (as we can't check)
		// and converts back from set of IEEE 754 floating point numbers back
		// to a string containing a JSON array of floating point numbers
		if len(v) > 0 {
			emb := ByteArrayToEmbedding(v)
			e.Embed = emb.JSON()
			return nil
		}
	case string:
		// Assumes that the dbms stores returns a string containing a JSON array of floating point numbers
		e.Embed = v
		return nil
	case float64:
		e.Embed = fmt.Sprintf("%f", v)
		return nil
	default:
		return fmt.Errorf("markers: not handled type %s", fmt.Sprintf("%T", value))
	}
	e.Embed = string(valueBytes)
	return nil
}

// ByteArrayToEmbedding converts a set of IEEE 754 floating point numbers back into an Embedding
func ByteArrayToEmbedding(values []byte) (result Embedding) {
	result = make(Embedding, len(values)/4)
	for i := range len(values) / 4 {
		start := i * 4
		end := start + 4
		word := binary.LittleEndian.Uint32(values[start:end])
		f32 := math.Float32frombits(word)
		result[i] = float64(f32)
	}
	return
}

// GormDataType returns the name of the data type for Gorm internal reference
func (e DBEmbed) GormDataType() string {
	return "vectorembedding"
}

// GormValue is used to insert/update the value into the database
func (e DBEmbed) GormValue(ctx context.Context, db *gorm.DB) clause.Expr {
	switch db.Name() {
	case "mysql":
		if v, ok := db.Dialector.(*mysql.Dialector); ok {
			if strings.Contains(v.ServerVersion, "MariaDB") {
				return clause.Expr{
					SQL:  "vec_fromtext(?)",
					Vars: []interface{}{e.Embed},
				}
			}
		}
		return clause.Expr{
			SQL:  "STRING_TO_VECTOR(?)",
			Vars: []interface{}{e.Embed},
		}
	default:
		return clause.Expr{
			SQL:  "?",
			Vars: []interface{}{e.Embed},
		}
	}
}

// ToDo: DBEmbedQueryExpression

type DBEmbedQueryExpression struct {
	column              string
	alias               string
	distance            bool
	equals              bool
	lessThan            bool
	greaterThan         bool
	lessThanOrEquals    bool
	greaterThanOrEquals bool
	distanceValue       float64
	embedValue          string
	equation            DistanceEquation
}

// DBEmbedQuery query column as vector
func DBEmbedQuery(column string) *DBEmbedQueryExpression {
	return &DBEmbedQueryExpression{column: column}
}

// Distance returns clause.Expression
func (dbembedQuery *DBEmbedQueryExpression) Distance(equation DistanceEquation, embed, alias string) *DBEmbedQueryExpression {
	dbembedQuery.distance = true
	dbembedQuery.alias = alias
	dbembedQuery.embedValue = embed
	dbembedQuery.equation = equation
	return dbembedQuery
}

// Equals returns clause.Expression
func (dbembedQuery *DBEmbedQueryExpression) Equals(equation DistanceEquation, distance float64, embed string) *DBEmbedQueryExpression {
	dbembedQuery.equals = true
	dbembedQuery.distanceValue = distance
	dbembedQuery.embedValue = embed
	dbembedQuery.equation = equation
	return dbembedQuery
}

// LessThan returns clause.Expression
func (dbembedQuery *DBEmbedQueryExpression) LessThan(equation DistanceEquation, distance float64, embed string) *DBEmbedQueryExpression {
	dbembedQuery.lessThan = true
	dbembedQuery.distanceValue = distance
	dbembedQuery.embedValue = embed
	dbembedQuery.equation = equation
	return dbembedQuery
}

// GreaterThan returns clause.Expression
func (dbembedQuery *DBEmbedQueryExpression) GreaterThan(equation DistanceEquation, distance float64, embed string) *DBEmbedQueryExpression {
	dbembedQuery.greaterThan = true
	dbembedQuery.distanceValue = distance
	dbembedQuery.embedValue = embed
	dbembedQuery.equation = equation
	return dbembedQuery
}

// LessThanOrEquals returns clause.Expression
func (dbembedQuery *DBEmbedQueryExpression) LessThanOrEquals(equation DistanceEquation, distance float64, embed string) *DBEmbedQueryExpression {
	dbembedQuery.lessThanOrEquals = true
	dbembedQuery.distanceValue = distance
	dbembedQuery.embedValue = embed
	dbembedQuery.equation = equation
	return dbembedQuery
}

// GreaterThanOrEquals returns clause.Expression
func (dbembedQuery *DBEmbedQueryExpression) GreaterThanOrEquals(equation DistanceEquation, distance float64, embed string) *DBEmbedQueryExpression {
	dbembedQuery.greaterThanOrEquals = true
	dbembedQuery.distanceValue = distance
	dbembedQuery.embedValue = embed
	dbembedQuery.equation = equation
	return dbembedQuery
}

// Build implements the clause.Expression
func (dbembedQuery *DBEmbedQueryExpression) Build(builder clause.Builder) {
	if stmt, ok := builder.(*gorm.Statement); ok {
		switch stmt.Name() {
		case "mysql":
			if v, ok := stmt.Dialector.(*mysql.Dialector); ok {
				if strings.Contains(v.ServerVersion, "MariaDB") {
					if dbembedQuery.equation == Distance_Cosine {
						builder.WriteString("VEC_DISTANCE_COSINE(") //nolint:errcheck // can't return the error
					} else {
						builder.WriteString("VEC_DISTANCE_EUCLIDEAN(") //nolint:errcheck // can't return the error
					}
					builder.WriteQuoted(dbembedQuery.column)
					builder.WriteString(", VEC_FromText(") //nolint:errcheck // can't return the error
					builder.AddVar(stmt, dbembedQuery.embedValue)
					builder.WriteString("))") //nolint:errcheck // can't return the error
					switch {
					case dbembedQuery.equals:
						builder.WriteString(" = ") //nolint:errcheck // can't return the error
					case dbembedQuery.lessThan:
						builder.WriteString(" < ") //nolint:errcheck // can't return the error
					case dbembedQuery.greaterThan:
						builder.WriteString(" > ") //nolint:errcheck // can't return the error
					case dbembedQuery.lessThanOrEquals:
						builder.WriteString(" <= ") //nolint:errcheck // can't return the error
					case dbembedQuery.greaterThanOrEquals:
						builder.WriteString(" >= ") //nolint:errcheck // can't return the error
					}
					if dbembedQuery.distance {
						builder.WriteString(" AS ")             //nolint:errcheck // can't return the error
						builder.WriteString(dbembedQuery.alias) //nolint:errcheck // can't return the error
					} else {
						stmt.AddVar(builder, dbembedQuery.distanceValue)
					}
				}
			} else {
				builder.WriteString("DISTANCE(") //nolint:errcheck // can't return the error
				builder.WriteQuoted(dbembedQuery.column)
				builder.WriteString(", STRING_TO_VECTOR(") //nolint:errcheck // can't return the error
				builder.AddVar(stmt, dbembedQuery.embedValue)
				builder.WriteString("), ") //nolint:errcheck // can't return the error
				if dbembedQuery.equation == Distance_Cosine {
					builder.AddVar(stmt, "COSINE)")
				} else {
					builder.AddVar(stmt, "EUCLIDEAN)")
				}

				switch {
				case dbembedQuery.equals:
					builder.WriteString(" = ") //nolint:errcheck // can't return the error
				case dbembedQuery.lessThan:
					builder.WriteString(" < ") //nolint:errcheck // can't return the error
				case dbembedQuery.greaterThan:
					builder.WriteString(" > ") //nolint:errcheck // can't return the error
				case dbembedQuery.lessThanOrEquals:
					builder.WriteString(" <= ") //nolint:errcheck // can't return the error
				case dbembedQuery.greaterThanOrEquals:
					builder.WriteString(" >= ") //nolint:errcheck // can't return the error
				}
				if dbembedQuery.distance {
					builder.WriteString(" AS ")             //nolint:errcheck // can't return the error
					builder.WriteString(dbembedQuery.alias) //nolint:errcheck // can't return the error
				} else {
					stmt.AddVar(builder, dbembedQuery.distanceValue)
				}
			}
		case "postgres":
			builder.WriteQuoted(dbembedQuery.column)
			if dbembedQuery.equation == Distance_Cosine {
				builder.WriteString(" <=> ") //nolint:errcheck // can't return the error
			} else {
				builder.WriteString(" <-> ") //nolint:errcheck // can't return the error
			}
			builder.AddVar(stmt, dbembedQuery.embedValue)
			switch {
			case dbembedQuery.equals:
				builder.WriteString(" = ") //nolint:errcheck // can't return the error
			case dbembedQuery.lessThan:
				builder.WriteString(" < ") //nolint:errcheck // can't return the error
			case dbembedQuery.greaterThan:
				builder.WriteString(" > ") //nolint:errcheck // can't return the error
			case dbembedQuery.lessThanOrEquals:
				builder.WriteString(" <= ") //nolint:errcheck // can't return the error
			case dbembedQuery.greaterThanOrEquals:
				builder.WriteString(" >= ") //nolint:errcheck // can't return the error
			}
			if dbembedQuery.distance {
				builder.WriteString(" AS ")             //nolint:errcheck // can't return the error
				builder.WriteString(dbembedQuery.alias) //nolint:errcheck // can't return the error
			} else {
				stmt.AddVar(builder, dbembedQuery.distanceValue)
			}
		case "sqlite":
			if dbembedQuery.distance {
				if dbembedQuery.equation == Distance_Cosine {
					builder.WriteString("vec_distance_cosine(") //nolint:errcheck // can't return the error
				} else {
					builder.WriteString("vec_distance_L2(") //nolint:errcheck // can't return the error
				}
				builder.WriteQuoted(dbembedQuery.column)
				builder.WriteString(", ") //nolint:errcheck // can't return the error
				builder.AddVar(stmt, dbembedQuery.embedValue)
				builder.WriteString(")") //nolint:errcheck // can't return the error
				switch {
				case dbembedQuery.equals:
					builder.WriteString(" = ") //nolint:errcheck // can't return the error
				case dbembedQuery.lessThan:
					builder.WriteString(" < ") //nolint:errcheck // can't return the error
				case dbembedQuery.greaterThan:
					builder.WriteString(" > ") //nolint:errcheck // can't return the error
				case dbembedQuery.lessThanOrEquals:
					builder.WriteString(" <= ") //nolint:errcheck // can't return the error
				case dbembedQuery.greaterThanOrEquals:
					builder.WriteString(" >= ") //nolint:errcheck // can't return the error
				}
				if dbembedQuery.distance {
					builder.WriteString(" AS ")             //nolint:errcheck // can't return the error
					builder.WriteString(dbembedQuery.alias) //nolint:errcheck // can't return the error
				} else {
					stmt.AddVar(builder, dbembedQuery.distanceValue)
				}
			} else {
				// sqlite-vec has some ugly limitations.
				// Must include a limit or a k = statement.
				// But, can't have a limit if using a join.
				// k = crops the results before doing the join, so if the row you want is k + 1, to bad.
				builder.WriteQuoted(dbembedQuery.column)
				builder.WriteString(" match ") //nolint:errcheck // can't return the error
				builder.AddVar(stmt, dbembedQuery.embedValue)
				// k = 5 is the equivalent of limit = 5
				builder.WriteString(" AND k = 50 ") //nolint:errcheck // can't return the error
				switch {
				case dbembedQuery.equals:
					builder.WriteString(" AND distance = ") //nolint:errcheck // can't return the error
				case dbembedQuery.lessThan:
					builder.WriteString(" AND distance < ") //nolint:errcheck // can't return the error
				case dbembedQuery.greaterThan:
					builder.WriteString(" AND distance > ") //nolint:errcheck // can't return the error
				case dbembedQuery.lessThanOrEquals:
					builder.WriteString(" AND distance <= ") //nolint:errcheck // can't return the error
				case dbembedQuery.greaterThanOrEquals:
					builder.WriteString(" AND distance >= ") //nolint:errcheck // can't return the error
				}
				stmt.AddVar(builder, dbembedQuery.distanceValue)
			}
		}
	}
}
