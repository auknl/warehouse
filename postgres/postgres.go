package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/auknl/warehouse/data"
	"github.com/auknl/warehouse/db"
	"github.com/auknl/warehouse/request"
	"github.com/sirupsen/logrus"
	"strconv"
)

//PInventoryDB keep db and configuration
type PInventoryDB struct {
	db     *sql.DB
	config Config
}

//Config keeps db related configurations
type Config struct {
	Logger   *logrus.Entry
	Driver   string
	Host     string
	Port     string
	User     string
	Password string
	Dbname   string
}

//NewPInventory creates new Postgres inventory instance
func NewPInventory(config Config) db.Inventory {
	config.Logger.Debug("NewPInventory entry...")
	inventory := PInventoryDB{config: config}
	err := inventory.Open()
	if err != nil {
		config.Logger.WithField("err: ", err).Error("Connection could not be set..")
	}

	return &inventory
}

//Ping verifies a connection to the database is still alive
func (inventory *PInventoryDB) Ping() error {
	inventory.config.Logger.Debug("Ping() entry...")
	return inventory.db.Ping()
	//TODO: if ping gives error, connection retry mech. can be added.
}

//Open opens a postgres database
func (inventory *PInventoryDB) Open() error {
	inventory.config.Logger.Debug("Open() entry...")
	psqlCredentials := fmt.Sprintf("host=%s port=%s user=%s "+
		"password=%s dbname=%s sslmode=disable",
		inventory.config.Host, inventory.config.Port, inventory.config.User, inventory.config.Password, inventory.config.Dbname)

	conn, err := sql.Open(inventory.config.Driver, psqlCredentials)
	if err != nil {
		inventory.config.Logger.WithField("err: ", err).Error("Sql open failed")
		return err
	}
	inventory.db = conn
	inventory.config.Logger.Debug("Open(), connection is set with db...")
	return nil
}

//GetInventory gets all inventory/stock info in system
func (inventory *PInventoryDB) GetInventory(ctx context.Context) (error, []data.Stock) {
	log := inventory.config.Logger.WithField("rid", request.GetRID(ctx))
	log.Debug("GetInventory() entry...")
	transaction, err := inventory.db.BeginTx(ctx, nil)
	if err != nil {
		log.WithField("err", err).Error("Transaction begin failed")
		return err, nil
	}
	defer transaction.Rollback() //get operation
	rows, err := transaction.Query(getInventory)
	if err != nil {
		log.WithField("err", err).Error("GetInventory query failed")
		return err, nil
	}

	defer rows.Close()
	var artId, artName string
	var stock string
	var stocks []data.Stock
	for rows.Next() {
		err = rows.Scan(&artId, &artName, &stock)
		if err != nil {
			log.WithField("err", err).Error("Cannot scan the table")
			return err, nil
		}
		stocks = append(stocks, data.Stock{ArtId: artId, Name: artName, Stock: stock})
	}

	err = rows.Err()
	if err != nil {
		log.WithField("err", err).Error("Error happened during the iteration")
		return err, nil
	}

	log.WithField("number of inventory record to be returned: ", len(stocks)).Debug("GetInventory(), returns the stocks...")
	return nil, stocks
}

//GetProductStock gets the stock of the available products in system
func (inventory *PInventoryDB) GetProductStock(ctx context.Context) (error, data.ProductStocks) {
	log := inventory.config.Logger.WithField("rid", request.GetRID(ctx))
	log.Debug("GetProductStock() entry...")
	transaction, err := inventory.db.BeginTx(ctx, nil)
	if err != nil {
		log.WithField("err", err).Error("Transaction begin failed")
		return err, nil
	}
	defer transaction.Rollback()
	rows, err := transaction.Query(getProductStock)
	if err != nil {
		log.WithField("err", err).Error("GetProductStock query failed")
		return err, nil
	}

	defer rows.Close()
	var productName string
	var stock string
	var stocks data.ProductStocks
	for rows.Next() {
		err = rows.Scan(&productName, &stock)
		if err != nil {
			log.WithField("err", err).Error("Cannot scan the table")
			return err, nil
		}
		stockNo, _ := strconv.ParseInt(stock, 10, 64)
		if stockNo != 0 { // if product items are enough
			stocks = append(stocks, data.ProductStock{Name: productName, AvailableProductNo: stock})
		}
	}

	err = rows.Err()
	if err != nil {
		log.WithField("err", err).Error("Error happened during the getProductStock iteration")
		return err, nil
	}

	log.WithField("number of product to be returned: ", len(stocks)).Debug("GetProductStock(), returns the stocks...")
	return nil, stocks
}

//UploadProducts inserts the product info into db
func (inventory *PInventoryDB) UploadProducts(ctx context.Context, product data.Products) (error, int) {
	log := inventory.config.Logger.WithField("rid", request.GetRID(ctx))
	log.Debug("UploadProducts() entry...")
	transaction, err := inventory.db.BeginTx(ctx, nil)
	if err != nil {
		log.WithField("err", err).Error("Transaction begin failed")
		return err, 0
	}
	insertedRecord := 0
	for _, product := range product.Products {
		for _, contain := range product.ContainArticles {
			_, err := transaction.ExecContext(ctx, insertProduct, product.Name, contain.ArtId, contain.AmountOf)
			if err != nil {
				transaction.Rollback()
				log.WithField("err: ", err).Error("UploadProducts(), failed to insert record...")
				return err, 0
				//TODO: Failed products can save and keep uploading till the end of list. Then the unsuccessful ones can serve the client
			}
		}
	}
	err = transaction.Commit()
	if err != nil {
		transaction.Rollback()
		log.WithField("err: ", err).Error("Transaction commit failed to insert product...")
	}
	insertedRecord = len(product.Products)

	log.WithField("number of product uploaded: ", insertedRecord).Debug("UploadProducts(), uploaded products...")
	return nil, insertedRecord
}

//UploadInventory inserts the inventory info into db
func (inventory *PInventoryDB) UploadInventory(ctx context.Context, inventoryToInsert data.Inventory) (error, int) {
	log := inventory.config.Logger.WithField("rid", request.GetRID(ctx))
	log.Debug("UploadInventory() entry...")
	transaction, err := inventory.db.BeginTx(ctx, nil)
	if err != nil {
		log.WithField("err", err).Error("Transaction begin failed")
		return err, 0
	}
	for _, inventoryRec := range inventoryToInsert.Inventory {
		_, err := transaction.ExecContext(ctx, insertStock, inventoryRec.ArtId, inventoryRec.Name, inventoryRec.Stock)
		if err != nil {
			transaction.Rollback()
			log.WithField("err: ", err).Error("UploadInventory failed to insert record...")
			return err, 0
		}
	}
	err = transaction.Commit()
	if err != nil {
		transaction.Rollback()
		log.WithField("err: ", err).Error("Failed to commit...")
		return nil, 0
	}
	insertedRecord := len(inventoryToInsert.Inventory)

	log.WithField("number of inventory uploaded: ", insertedRecord).Debug("UploadInventory(), uploaded products...")
	return nil, insertedRecord
}

//SellProduct checks if the product exist and in stock. If true then update inventory accordingly
func (inventory *PInventoryDB) SellProduct(ctx context.Context, productName string) error {
	log := inventory.config.Logger.WithField("rid", request.GetRID(ctx))
	log.Debug("sellProduct() entry...")
	transaction, err := inventory.db.BeginTx(ctx, nil)
	if err != nil {
		log.WithField("err", err).Error("Transaction begin failed")
		return err
	}

	defer transaction.Rollback()
	// do not sell if the product does not exist
	rows, errQuery := transaction.Query(productExist, productName)
	if errQuery != nil {
		log.WithField("err", err).Error("ProductExist query failed")
		return err
	}
	var productExist int
	for rows.Next() {
		err = rows.Scan(&productExist)
		if err != nil {
			log.WithField("err", err).Error("Cannot scan the table")
			return err
		}
		if productExist == 0 {
			log.WithField("err", err).Info("product is not found in system")
			return errors.New("this product is not in system, cannot be sold")
		}
	}

	// do not sell if the product is not in stock
	rows, errQuery = transaction.Query(inStock, productName)
	if errQuery != nil {
		log.WithField("err", err).Error("InStock query failed")
		return err
	}
	var stockNo int
	for rows.Next() {
		err = rows.Scan(&stockNo)
		if err != nil {
			log.WithField("err", err).Error("Cannot scan the table")
			return err
		}
		if stockNo != 0 {
			log.WithField("err", err).Info("product items are out of stock")
			return errors.New("this product is not in stock, cannot be sold")
		}
	}

	defer rows.Close()
	_, err = transaction.ExecContext(ctx, updateSaleInfo, productName)
	if err != nil {
		transaction.Rollback()
		log.WithField("err: ", err).Error("SellProduct(), failed to update inventory...")
		return err
	}
	err = transaction.Commit()
	if err != nil {
		transaction.Rollback()
		log.WithField("err: ", err).Error("SellProduct(), failed to commit...")
		return err
	}

	log.WithField("product is sold: ", productName).Debug("sellProduct(), sold the product and update the inventory...")
	return nil
}
