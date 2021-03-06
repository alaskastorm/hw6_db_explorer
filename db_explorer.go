package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

type Handler struct {
	DB     *sql.DB
	Output interface{}
}

type DB struct {
	TableName string
	Name      string
}

type Response map[string]interface{}

type Columns struct {
	MetaData       map[string]interface{}
	NamesWithTypes map[string]interface{}
	IDNameColumn   string
}

func NewQuery(db *sql.DB, query string) (Columns, error) {
	columns := Columns{}
	columns.MetaData = make(map[string]interface{})
	columns.NamesWithTypes = make(map[string]interface{})

	result, err := db.Query(query)
	if err != nil {
		log.Println(err)

		return Columns{}, err
	}

	for result.Next() {
		cols, err := result.ColumnTypes()
		if err != nil {
			log.Println(err)

			return Columns{}, err
		}

		for _, v := range cols {
			columnType := v.DatabaseTypeName()
			switch columnType {
			case "TEXT", "VARCHAR":
				if nullable, _ := v.Nullable(); nullable {
					columns.MetaData[v.Name()] = new(sql.NullString)

					break
				}
				columns.MetaData[v.Name()] = new(string)
				columns.NamesWithTypes[v.Name()] = ""
			case "INT":
				columns.IDNameColumn = v.Name()
				if nullable, _ := v.Nullable(); nullable {
					columns.MetaData[v.Name()] = new(sql.NullInt32)

					break
				}
				columns.MetaData[v.Name()] = new(int32)
			}
		}
	}

	result.Close()

	return columns, nil

}

func NewDbExplorer(db *sql.DB) (http.Handler, error) {

	testHandler := &Handler{DB: db}

	return testHandler, nil
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	log.Println("URL PATH", r.URL.Path)

	switch r.Method {
	case "GET":
		h.Read(w, r)
	case "PUT", "POST":
		h.CreateAndUpdate(w, r)
	case "DELETE":
		h.Delete(w, r)
	default:
		response, _ := json.Marshal(&Response{
			"error": "unknown table",
		})

		w.WriteHeader(http.StatusNotFound)
		w.Write(response)
	}
}

func (h *Handler) Read(w http.ResponseWriter, r *http.Request) {
	log.Println("READ:", r.URL.Path)

	var db = h.DB

	tableNames, err := getTableNames(db)
	if err != nil {
		log.Println(err)
		internalServerError(w)

		return
	}

	reqTableName := r.URL.Path

	switch reqTableName {
	case "/":
		response, err := json.Marshal(&Response{
			"response": Response{
				"tables": tableNames,
			},
		})
		if err != nil {
			log.Println(err)
			internalServerError(w)

			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(response)

	default:
		reTableName := regexp.MustCompile(`/[\w]*[/?]?`).FindString(reqTableName)
		reWithID := regexp.MustCompile(`/[\w]*/[\d]*`).FindString(reqTableName)
		reqTableName = strings.Trim(reTableName, "/")
		ID := strings.Split(reWithID, "/")

		for _, tableName := range tableNames {
			if tableName == reqTableName {
				query := fmt.Sprintf("SELECT * FROM %s ", tableName)
				columns, err := NewQuery(db, query)
				if err != nil {
					log.Println(err)
					internalServerError(w)

					return
				}

				if len(ID) > 1 {
					query = fmt.Sprintf(
						"SELECT * FROM %s WHERE %s = %s",
						tableName, columns.IDNameColumn, ID[2])
					data, err := getDataFromDB(h, query)
					if err != nil {
						log.Println(err)
						internalServerError(w)

						return
					}

					if len(data.([]interface{})) > 0 {
						for _, data := range data.([]interface{}) {
							response, err := json.Marshal(&Response{
								"response": Response{
									"record": data,
								},
							})
							if err != nil {
								log.Println(err)
								internalServerError(w)

								return
							}

							w.WriteHeader(http.StatusOK)
							w.Write(response)
						}

						return
					}

					response, _ := json.Marshal(&Response{
						"error": "record not found",
					})

					w.WriteHeader(http.StatusNotFound)
					w.Write(response)

					return

				} else {
					if isExistsParam(r, "offset") {
						param, err := strconv.Atoi(r.FormValue("offset"))
						if err != nil {
							offset := 0
							query += fmt.Sprintf("WHERE %s > %d ", columns.IDNameColumn, offset)
						} else {
							offset := param
							query += fmt.Sprintf("WHERE %s > %d ", columns.IDNameColumn, offset)
						}
					}

					if isExistsParam(r, "limit") {
						param, err := strconv.Atoi(r.FormValue("limit"))
						if err != nil {
							limit := 5
							query += fmt.Sprintf("LIMIT %d", limit)
						} else {
							limit := param
							query += fmt.Sprintf("LIMIT %d", limit)
						}
					}

					data, err := getDataFromDB(h, query)
					if err != nil {
						log.Println(err)
						internalServerError(w)

						return
					}

					response, err := json.Marshal(&Response{
						"response": Response{
							"records": data,
						},
					})
					if err != nil {
						log.Println(err)
						internalServerError(w)

						return
					}

					w.WriteHeader(http.StatusOK)
					w.Write(response)

					return
				}

			}

		}

		response, _ := json.Marshal(&Response{
			"error": "unknown table",
		})

		w.WriteHeader(http.StatusNotFound)
		w.Write(response)
	}

}

// Together because of tests
func (h *Handler) CreateAndUpdate(w http.ResponseWriter, r *http.Request) {
	log.Println("CREATE and UPDATE:", r.URL.Path)

	var (
		data    interface{}
		db      = h.DB
		reqPath = r.URL.Path
	)

	tableNames, err := getTableNames(db)
	if err != nil {
		log.Println(err)
		internalServerError(w)

		return
	}

	reTableName := regexp.MustCompile(`/[\w]*[/?]?`).FindString(reqPath)
	reWithID := regexp.MustCompile(`/[\w]*/[\d]*`).FindString(reqPath)
	reqTableName := strings.Trim(reTableName, "/")

	for _, tableName := range tableNames {
		if tableName == reqTableName {
			body, err := ioutil.ReadAll(r.Body)
			if err != nil {
				log.Println(err)
				internalServerError(w)

				return
			}

			if err = json.Unmarshal(body, &data); err != nil {
				log.Println(err)
				internalServerError(w)

				return
			}

			query := fmt.Sprintf("SELECT * FROM %s", tableName)

			columns, err := NewQuery(db, query)
			if err != nil {
				log.Println(err)
				internalServerError(w)

				return
			}

			var Fields struct {
				fields map[string]interface{}
			}

			Fields.fields = make(map[string]interface{})
			f := Fields.fields

			exists, err := regexp.MatchString(`/[\d].*`, reWithID)
			if err != nil {
				log.Println(err)
				internalServerError(w)

				return
			}

			if exists {
				ID := strings.Split(reWithID, "/")[2]
				for k, v := range data.(map[string]interface{}) {
					switch v.(type) {
					case float64:
						response, _ := json.Marshal(&Response{
							"error": "field " + k + " have invalid type",
						})

						w.WriteHeader(http.StatusBadRequest)
						w.Write(response)

						return
					case string:
						field := v.(string)
						fieldFromDB := columns.MetaData[k]

						switch fieldFromDB.(type) {
						case *string, *sql.NullString:
							f[k] = field
						default:
							response, _ := json.Marshal(&Response{
								"error": "field " + k + " have invalid type",
							})

							w.WriteHeader(http.StatusBadRequest)
							w.Write(response)

							return
						}
					case nil:
						fieldFromDB := columns.MetaData[k]

						switch fieldFromDB.(type) {
						case *sql.NullString:
							f[k] = sql.NullString{}
						case *sql.NullInt32:
							f[k] = sql.NullInt32{}
						default:
							response, _ := json.Marshal(&Response{
								"error": "field " + k + " have invalid type",
							})

							w.WriteHeader(http.StatusBadRequest)
							w.Write(response)

							return
						}
					default:
						log.Println("default", v, k)
					}

				}

				var (
					values []interface{}
					fs     []string
				)

				for k, _ := range f {
					fs = append(fs, k)
				}

				query = fmt.Sprintf("UPDATE %s SET ", tableName)

				for i, v := range fs {
					values = append(values, f[v])

					if i == len(fs)-1 {
						query += fmt.Sprintf("%s = ? ", v)

						break
					}

					query += fmt.Sprintf("%s = ?, ", v)
				}

				res, err := db.Exec(query+fmt.Sprintf("WHERE %s = %s", columns.IDNameColumn, ID), values...)
				if err != nil {
					log.Println(err)
					internalServerError(w)

					return
				}

				affected, err := res.RowsAffected()
				if err != nil {
					log.Println(err)
					internalServerError(w)

					return
				}

				response, _ := json.Marshal(&Response{
					"response": Response{
						"updated": affected,
					},
				})

				w.WriteHeader(http.StatusOK)
				w.Write(response)

				return
			}

			for k, v := range data.(map[string]interface{}) {
				switch v.(type) {
				case string:
					field := v.(string)
					fieldFromDB, exists := columns.MetaData[k]
					if exists {
						switch fieldFromDB.(type) {
						case *string, *sql.NullString:
							columns.NamesWithTypes[k] = field
						default:
							log.Println(field)
							response, _ := json.Marshal(&Response{
								"error": "field " + k + " have invalid type",
							})

							w.WriteHeader(http.StatusBadRequest)
							w.Write(response)

							return
						}
					}
				case nil:
					fieldFromDB := columns.MetaData[k]

					switch fieldFromDB.(type) {
					case *sql.NullString:
						columns.NamesWithTypes[k] = sql.NullString{}
					case *sql.NullInt32:
						columns.NamesWithTypes[k] = sql.NullInt32{}
					default:
						response, _ := json.Marshal(&Response{
							"error": "field " + k + " have invalid type",
						})

						w.WriteHeader(http.StatusBadRequest)
						w.Write(response)

						return
					}

				default:
					log.Println("default", v)
				}

			}

			var (
				values     []interface{}
				params, fs []string
			)

			for k, v := range columns.NamesWithTypes {
				fs = append(fs, k)
				params = append(params, "?")
				values = append(values, fmt.Sprintf("%s", v))
			}

			query = fmt.Sprintf("INSERT INTO %s (", tableName) +
				strings.Join(fs, ", ") + ") VALUES (" + strings.Join(params, ", ") + ")"

			res, err := db.Exec(query, values...)
			if err != nil {
				log.Println(err)
				internalServerError(w)

				return
			}

			affected, err := res.LastInsertId()
			if err != nil {
				log.Println(err)
				internalServerError(w)

				return
			}

			response, _ := json.Marshal(&Response{
				"response": Response{
					columns.IDNameColumn: affected,
				},
			})

			w.WriteHeader(http.StatusOK)
			w.Write(response)

			return
		}

	}

	response, _ := json.Marshal(&Response{
		"error": "unknown table",
	})

	w.WriteHeader(http.StatusOK)
	w.Write(response)
}

func (h *Handler) Delete(w http.ResponseWriter, r *http.Request) {
	log.Println("DELETE:", r.URL.Path)

	var (
		db      = h.DB
		reqPath = r.URL.Path
	)

	tableNames, err := getTableNames(db)
	if err != nil {
		log.Println(err)
		internalServerError(w)

		return
	}

	reTableName := regexp.MustCompile(`/[\w]*[/?]?`).FindString(reqPath)
	reWithID := regexp.MustCompile(`/[\w]*/[\d].*`).FindString(reqPath)
	reqTableName := strings.Trim(reTableName, "/")

	for _, tableName := range tableNames {
		if tableName == reqTableName {
			exists, err := regexp.MatchString(`/[\d].*`, reWithID)
			if err != nil {
				log.Println(err)
				internalServerError(w)

				return
			}

			if exists {
				ID := strings.Split(reWithID, "/")[2]
				query := fmt.Sprintf("DELETE FROM %s WHERE id=%s", tableName, ID)

				result, err := db.Exec(query)
				if err != nil {
					log.Println(err)
					internalServerError(w)

					return
				}

				affected, err := result.RowsAffected()
				if err != nil {
					log.Println(err)
					internalServerError(w)

					return
				}

				response, _ := json.Marshal(&Response{
					"response": Response{
						"deleted": affected,
					},
				})

				w.WriteHeader(http.StatusOK)
				w.Write(response)

				return
			}
		}
	}
}

func getTableNames(db *sql.DB) ([]string, error) {

	rows, err := db.Query("SHOW TABLES")
	if err != nil {
		log.Printf("%#v\n", err)
		return nil, err
	}

	var tableNames []string

	for rows.Next() {
		data := DB{}

		if err := rows.Scan(&data.TableName); err != nil {
			log.Printf("%#v\n", err)
			return nil, err
		}

		tableNames = append(tableNames, data.TableName)
	}

	return tableNames, nil
}

func internalServerError(w http.ResponseWriter) {

	response, _ := json.Marshal(&Response{
		"error": "internal server error",
	})

	w.WriteHeader(http.StatusInternalServerError)
	w.Write(response)
}

func isExistsParam(r *http.Request, key string) bool {

	param := r.FormValue(key)
	if len(param) > 0 {
		return true
	}

	return false
}

func getDataFromDB(h *Handler, query string) (interface{}, error) {

	result, err := h.DB.Query(query)
	if err != nil {
		log.Println("RESULT:", err)
		return "", err
	}

	var output []interface{}

	for result.Next() {
		dataMap := make(map[string]interface{})
		columnNames, err := result.Columns()
		if err != nil {
			log.Println("COLUMNS:", err)
			return "", err
		}

		data := make([]interface{}, len(columnNames))

		columns, err := result.ColumnTypes()
		if err != nil {
			log.Println("ERROR COLUMNS")
			return "", err
		}

		for i, v := range columns {
			columnType := v.DatabaseTypeName()
			switch columnType {
			case "TEXT", "VARCHAR":
				if nullable, _ := v.Nullable(); nullable {
					data[i] = new(sql.NullString)
					dataMap[v.Name()] = data[i]

					break
				}

				data[i] = new(string)
				dataMap[v.Name()] = data[i]
			case "INT":
				if nullable, _ := v.Nullable(); nullable {
					data[i] = new(sql.NullInt32)
					dataMap[v.Name()] = data[i]

					break
				}

				data[i] = new(int)
				dataMap[v.Name()] = data[i]
			}
		}

		if err := result.Scan(data...); err != nil {
			log.Println(err)
			return "", err
		}

		output = append(output, dataMap)
	}

	result.Close()

	for _, v := range output {
		oneSet := v.(map[string]interface{})
		for key, val := range oneSet {
			switch val.(type) {
			case *string:
				valType := val.(*string)
				value := *valType
				oneSet[key] = value
			case *int:
				valType := val.(*int)
				value := *valType
				oneSet[key] = value
			case *sql.NullString:
				valType := val.(*sql.NullString)
				value := *valType
				if value.Valid {
					oneSet[key] = value.String

					break
				}

				oneSet[key] = nil
			case *sql.NullInt32:
				valType := val.(*sql.NullInt32)
				value := *valType
				if value.Valid {
					oneSet[key] = value.Int32

					break
				}

				oneSet[key] = nil
			}
		}
	}
	return output, nil
}
