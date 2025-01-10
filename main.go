package main

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	_ "github.com/mattn/go-sqlite3"
)

var (
	db              *sql.DB
	userStates      = make(map[int64]string) // userID -> state
	selectedQueueID = make(map[int64]int)    // userID -> queueID
	queueActions    = make(map[int64]string) // userID -> action type (для администрирования и просмотра)
)

func main() {
	botToken := os.Getenv("API_KEY")
	if botToken == "" {
		log.Fatalf("Токен не найден!")
	}

	bot, err := tgbotapi.NewBotAPI(botToken)
	if err != nil {
		log.Panic(err)
	}

	bot.Debug = true
	log.Printf("Бот авторизован на аккаунте %s", bot.Self.UserName)

	db, err = initDB()
	if err != nil {
		log.Fatalf("Ошибка базы данных: %v", err)
	}
	defer db.Close()

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := bot.GetUpdatesChan(u)

	for update := range updates {
		if update.Message != nil {
			handleMessage(bot, update.Message)
		} else if update.CallbackQuery != nil {
			handleCallback(bot, update.CallbackQuery)
		}
	}
}

// Инициализация базы данных
func initDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", "queues.db")
	if err != nil {
		return nil, err
	}

	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS queues (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT NOT NULL,
		created_by INTEGER,
		created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	);
	CREATE TABLE IF NOT EXISTS queue_entries (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		queue_id INTEGER,
		user_id INTEGER,
		username TEXT,
		joined_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
		FOREIGN KEY(queue_id) REFERENCES queues(id)
	);
	`)
	return db, err
}

// Обработка входящих сообщений
func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	userID := message.Chat.ID

	switch userStates[userID] {
	case "select_queue_for_action":
		handleQueueActionSelection(bot, message)
	case "creating_queue":
		handleQueueCreation(bot, message) // Пользователь вводит название новой очереди
	case "admin_delete_user":
		deleteUserFromQueue(bot, message)
	case "admin_mode":
		handleAdminActions(bot, message) // Обработка администраторских действий
	// case "admin_delete_user":
	// 	handleDeleteUser(bot, message)
	default:
		showMainMenu(bot, message)
	}
}

func deleteUserFromQueue(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	username := message.Text
	chatID := message.Chat.ID
	queueID, exists := selectedQueueID[chatID]
	if !exists {
		msg := tgbotapi.NewMessage(chatID, "Ошибка: очередь не выбрана.")
		msg.ReplyMarkup = mainMenu()
		bot.Send(msg)
		return
	}

	// Удаляем пользователя из очереди
	res, err := db.Exec("DELETE FROM queue_entries WHERE queue_id = ? AND username = ?", queueID, username)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Ошибка при удалении пользователя.")
		msg.ReplyMarkup = mainMenu()
		bot.Send(msg)
		return
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		msg := tgbotapi.NewMessage(chatID, "Пользователь не найден в этой очереди.")
		msg.ReplyMarkup = mainMenu()
		bot.Send(msg)
	} else {
		msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Пользователь \"%s\" успешно удалён из очереди.", username))
		msg.ReplyMarkup = mainMenu()
		bot.Send(msg)
	}

	delete(userStates, chatID) // Сброс состояния
	delete(queueActions, message.Chat.ID)
}

func handleAdminActions(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	chatID := message.Chat.ID
	queueID, exists := selectedQueueID[chatID]
	if !exists {
		msg := tgbotapi.NewMessage(chatID, "Ошибка: очередь не выбрана.")
		msg.ReplyMarkup = mainMenu()
		bot.Send(msg)
		return
	}

	defer func() {
		if userStates[chatID] != "admin_delete_user" {
			delete(userStates, message.Chat.ID)
			delete(queueActions, message.Chat.ID)
		}
	}()

	switch message.Text {
	case "Очистить очередь":
		clearQueue(bot, chatID, queueID)
	case "Удалить очередь":
		deleteQueue(bot, chatID, queueID)
	case "Удалить пользователя из очереди":
		userStates[chatID] = "admin_delete_user"
		msg := tgbotapi.NewMessage(chatID, "Введите username пользователя для удаления из очереди:")
		bot.Send(msg)
	case "Назад в главное меню":
		delete(userStates, chatID)
		delete(selectedQueueID, chatID)
		msg := tgbotapi.NewMessage(chatID, "Возвращаю в главное меню:")
		msg.ReplyMarkup = mainMenu()
		bot.Send(msg)
	default:
		msg := tgbotapi.NewMessage(chatID, "Неверная команда. Используйте меню.")
		bot.Send(msg)
	}
}

func clearQueue(bot *tgbotapi.BotAPI, chatID int64, queueID int) {
	_, err := db.Exec("DELETE FROM queue_entries WHERE queue_id = ?", queueID)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Ошибка при очистке очереди.")
		bot.Send(msg)
		return
	}

	msg := tgbotapi.NewMessage(chatID, "Очередь успешно очищена.")
	msg.ReplyMarkup = mainMenu()
	bot.Send(msg)
}

func deleteQueue(bot *tgbotapi.BotAPI, chatID int64, queueID int) {
	// Удаляем очередь и все её записи
	_, err := db.Exec("DELETE FROM queues WHERE id = ?", queueID)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Ошибка при удалении очереди.")
		msg.ReplyMarkup = mainMenu()
		bot.Send(msg)
		return
	}

	_, err = db.Exec("DELETE FROM queue_entries WHERE queue_id = ?", queueID)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Ошибка при удалении записей из очереди.")
		msg.ReplyMarkup = mainMenu()
		bot.Send(msg)
		return
	}

	delete(selectedQueueID, chatID) // Удаляем выбранную очередь из состояния пользователя

	msg := tgbotapi.NewMessage(chatID, "Очередь успешно удалена.")
	msg.ReplyMarkup = mainMenu()
	bot.Send(msg)
}

// Удаление конкретного пользователя
func handleDeleteUser(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	username := message.Text
	queueID := selectedQueueID[message.Chat.ID]

	res, err := db.Exec("DELETE FROM queue_entries WHERE queue_id = ? AND username = ?", queueID, username)
	if err != nil {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Ошибка при удалении пользователя.")
		msg.ReplyMarkup = mainMenu()
		bot.Send(msg)
		return
	}

	rowsAffected, _ := res.RowsAffected()
	if rowsAffected == 0 {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Пользователь не найден в этой очереди.")
		msg.ReplyMarkup = mainMenu()
		bot.Send(msg)
	} else {
		msg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("Пользователь \"%s\" удален из очереди.", username))
		msg.ReplyMarkup = mainMenu()
		bot.Send(msg)
	}

	delete(userStates, message.Chat.ID) // Сброс состояния
}

// Главное меню
func showMainMenu(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	switch message.Text {
	case "/start":
		msg := tgbotapi.NewMessage(message.Chat.ID, "Добро пожаловать! Выберите действие:")
		msg.ReplyMarkup = mainMenu()
		bot.Send(msg)

	case "Зайти в очередь":
		userStates[message.Chat.ID] = "select_queue_for_action"
		queueActions[message.Chat.ID] = "join"
		showQueues(bot, message.Chat.ID)

	case "Показать очередь":
		userStates[message.Chat.ID] = "select_queue_for_action"
		queueActions[message.Chat.ID] = "show"
		showQueues(bot, message.Chat.ID)

	case "Создать очередь":
		userStates[message.Chat.ID] = "creating_queue"
		msg := tgbotapi.NewMessage(message.Chat.ID, "Введите название новой очереди:")
		bot.Send(msg)

	case "Изменить очередь (Админ)":
		userStates[message.Chat.ID] = "select_queue_for_action"
		queueActions[message.Chat.ID] = "admin"
		showQueues(bot, message.Chat.ID)

	default:
		msg := tgbotapi.NewMessage(message.Chat.ID, "Неверная команда. Пожалуйста, используйте меню.")
		bot.Send(msg)
	}
}

func mainMenu() tgbotapi.ReplyKeyboardMarkup {
	buttons := [][]tgbotapi.KeyboardButton{
		{tgbotapi.NewKeyboardButton("Зайти в очередь")},
		{tgbotapi.NewKeyboardButton("Показать очередь")},
		{tgbotapi.NewKeyboardButton("Создать очередь")},
		{tgbotapi.NewKeyboardButton("Изменить очередь (Админ)")},
		{tgbotapi.NewKeyboardButton("Назад в главное меню")},
	}
	return tgbotapi.NewReplyKeyboard(buttons...)
}

// Показать список очередей
func showQueues(bot *tgbotapi.BotAPI, chatID int64) {
	rows, err := db.Query("SELECT id, name FROM queues")
	if err != nil {
		log.Printf("Ошибка при загрузке очередей: %v", err)
		msg := tgbotapi.NewMessage(chatID, "Ошибка при загрузке очередей.")
		bot.Send(msg)
		return
	}
	defer rows.Close()

	var buttons [][]tgbotapi.KeyboardButton
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err == nil {
			buttons = append(buttons, tgbotapi.NewKeyboardButtonRow(
				tgbotapi.NewKeyboardButton(name),
			))
		}
	}

	if len(buttons) == 0 {
		msg := tgbotapi.NewMessage(chatID, "Очередей пока нет.")
		bot.Send(msg)
		return
	}

	msg := tgbotapi.NewMessage(chatID, "Выберите очередь:")
	msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(buttons...)
	bot.Send(msg)
}

func handleQueueActionSelection(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	queueName := message.Text
	if queueName == "Назад в главное меню" {

		msg := tgbotapi.NewMessage(message.Chat.ID, "Возвращаю в главное меню")
		msg.ReplyMarkup = mainMenu()
		bot.Send(msg)
		delete(userStates, message.Chat.ID)
		delete(queueActions, message.Chat.ID)
		return
	}
	var queueID int

	err := db.QueryRow("SELECT id FROM queues WHERE name = ?", queueName).Scan(&queueID)
	if err != nil {
		msg := tgbotapi.NewMessage(message.Chat.ID, "Такой очереди не существует. Попробуйте снова.")
		bot.Send(msg)
		return
	}

	switch queueActions[message.Chat.ID] {
	case "join":
		addUserToQueue(bot, message.Chat.ID, queueID, message.From.UserName)
	case "show":
		showQueueEntries(bot, message.Chat.ID, queueID)
	case "admin":

		adminQueueMenu(bot, message.Chat.ID, queueID)
	}
	if queueActions[message.Chat.ID] != "admin" {
		delete(userStates, message.Chat.ID)
		delete(queueActions, message.Chat.ID)
	}
}

func adminQueueMenu(bot *tgbotapi.BotAPI, chatID int64, queueID int) {
	userStates[chatID] = "admin_mode"
	// Сохраняем ID выбранной очереди для администраторских действий
	selectedQueueID[chatID] = queueID
	log.Println("Пизды")
	// Формируем сообщение с меню администратора
	msg := tgbotapi.NewMessage(chatID, "Вы вошли в режим управления очередью. Выберите действие:")
	msg.ReplyMarkup = adminMenuKeyboard()
	bot.Send(msg)
}

// Клавиатура для меню администратора
func adminMenuKeyboard() tgbotapi.ReplyKeyboardMarkup {
	buttons := [][]tgbotapi.KeyboardButton{
		{tgbotapi.NewKeyboardButton("Очистить очередь")},
		{tgbotapi.NewKeyboardButton("Удалить очередь")},
		{tgbotapi.NewKeyboardButton("Удалить пользователя из очереди")},
		{tgbotapi.NewKeyboardButton("Назад в главное меню")},
	}
	return tgbotapi.NewReplyKeyboard(buttons...)
}

func addUserToQueue(bot *tgbotapi.BotAPI, chatID int64, queueID int, username string) {
	_, err := db.Exec("INSERT INTO queue_entries (queue_id, user_id, username) VALUES (?, ?, ?)", queueID, chatID, username)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Ошибка при добавлении в очередь.")
		bot.Send(msg)
		return
	}
	msg := tgbotapi.NewMessage(chatID, fmt.Sprintf("Вы добавлены в очередь!"))
	msg.ReplyMarkup = mainMenu()
	bot.Send(msg)
}

func showQueueEntries(bot *tgbotapi.BotAPI, chatID int64, queueID int) {
	rows, err := db.Query("SELECT username FROM queue_entries WHERE queue_id = ?", queueID)
	if err != nil {
		msg := tgbotapi.NewMessage(chatID, "Ошибка при получении списка очереди.")
		bot.Send(msg)
		return
	}
	defer rows.Close()

	var users []string
	for rows.Next() {
		var username string
		rows.Scan(&username)
		users = append(users, "@"+username)
	}

	msgText := "Состав очереди:\n" + strings.Join(users, "\n")
	msg := tgbotapi.NewMessage(chatID, msgText)
	msg.ReplyMarkup = mainMenu()
	bot.Send(msg)
}

// package main
//
// import (
//
//	"database/sql"
//	"fmt"
//	"log"
//	"strings"
//
//	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
//
//	_ "github.com/mattn/go-sqlite3"
//
// )
//
// var (
//
//	db         *sql.DB
//	userStates = make(map[int64]string) // Состояния пользователей: userID -> state
//
// )
//
//	func main() {
//		bot, err := tgbotapi.NewBotAPI(botToken)
//		if err != nil {
//			log.Panic(err)
//		}
//
//		bot.Debug = true
//		log.Printf("Бот авторизован на аккаунте %s", bot.Self.UserName)
//
//		// Инициализация базы данных
//		db, err = initDB()
//		if err != nil {
//			log.Fatalf("Ошибка подключения к базе данных: %v", err)
//		}
//		defer db.Close()
//
//		u := tgbotapi.NewUpdate(0)
//		u.Timeout = 60
//		updates := bot.GetUpdatesChan(u)
//
//		for update := range updates {
//			if update.Message != nil {
//				handleMessage(bot, update.Message)
//			} else if update.CallbackQuery != nil {
//				handleCallback(bot, update.CallbackQuery)
//			}
//		}
//	}
//
// // Инициализация базы данных
//
//	func initDB() (*sql.DB, error) {
//		db, err := sql.Open("sqlite3", "queues.db")
//		if err != nil {
//			return nil, err
//		}
//
//		_, err = db.Exec(`
//		CREATE TABLE IF NOT EXISTS queues (
//			id INTEGER PRIMARY KEY AUTOINCREMENT,
//			name TEXT NOT NULL,
//			description TEXT,
//			created_by INTEGER,
//			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
//		);
//		CREATE TABLE IF NOT EXISTS queue_entries (
//			id INTEGER PRIMARY KEY AUTOINCREMENT,
//			queue_id INTEGER,
//			user_id INTEGER,
//			username TEXT,
//			joined_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
//			FOREIGN KEY(queue_id) REFERENCES queues(id)
//		);
//		`)
//		if err != nil {
//			return nil, err
//		}
//
//		return db, nil
//	}
//
// // Обработка сообщений
//
//	func handleMessage(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
//		userID := message.Chat.ID
//
//		// Проверяем текущее состояние пользователя
//		switch userStates[userID] {
//		case "waiting_for_queue_choice":
//			handleQueueSelection(bot, message) // Пользователь выбирает очередь для записи
//		case "creating_queue":
//			handleQueueCreation(bot, message) // Пользователь вводит название новой очереди
//		case "showing_queue":
//			handleQueueShow(bot, message) // Пользователь выбирает очередь для отображения
//		default:
//			handleMainMenu(bot, message) // Обработка команд из главного меню
//		}
//	}
//
// // Главное меню
//
//	func handleMainMenu(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
//		switch message.Text {
//		case "/start":
//			msg := tgbotapi.NewMessage(message.Chat.ID, "Добро пожаловать! Выберите действие:")
//			msg.ReplyMarkup = mainMenu()
//			bot.Send(msg)
//
//		case "Зайти в очередь":
//			userStates[message.Chat.ID] = "waiting_for_queue_choice"
//			showQueues(bot, message.Chat.ID)
//
//		case "Создать очередь":
//			userStates[message.Chat.ID] = "creating_queue"
//			msg := tgbotapi.NewMessage(message.Chat.ID, "Введите название новой очереди:")
//			bot.Send(msg)
//
//		case "Показать очередь":
//			userStates[message.Chat.ID] = "showing_queue"
//			showQueues(bot, message.Chat.ID)
//
//		case "Изменить очередь (Админ)":
//			msg := tgbotapi.NewMessage(message.Chat.ID, "Выберите очередь для редактирования (функция в разработке).")
//			bot.Send(msg)
//
//		default:
//			msg := tgbotapi.NewMessage(message.Chat.ID, "Я не понимаю эту команду. Выберите действие из меню.")
//			bot.Send(msg)
//		}
//	}
//
// // Главное меню (кнопки)
//
//	func mainMenu() tgbotapi.ReplyKeyboardMarkup {
//		buttons := [][]tgbotapi.KeyboardButton{
//			{tgbotapi.NewKeyboardButton("Зайти в очередь")},
//			{tgbotapi.NewKeyboardButton("Создать очередь")},
//			{tgbotapi.NewKeyboardButton("Показать очередь")},
//			{tgbotapi.NewKeyboardButton("Изменить очередь (Админ)")},
//		}
//		return tgbotapi.NewReplyKeyboard(buttons...)
//	}
//
// // Показать очереди для выбора
//
//	func showQueues(bot *tgbotapi.BotAPI, chatID int64) {
//		rows, err := db.Query("SELECT id, name FROM queues")
//		if err != nil {
//			log.Printf("Ошибка при загрузке очередей: %v", err)
//			msg := tgbotapi.NewMessage(chatID, "Ошибка при загрузке очередей.")
//			bot.Send(msg)
//			return
//		}
//		defer rows.Close()
//
//		var buttons [][]tgbotapi.KeyboardButton
//		for rows.Next() {
//			var id int
//			var name string
//			err := rows.Scan(&id, &name)
//			if err != nil {
//				log.Printf("Ошибка при обработке очередей: %v", err)
//				continue
//			}
//			buttons = append(buttons, tgbotapi.NewKeyboardButtonRow(
//				tgbotapi.NewKeyboardButton(name),
//			))
//		}
//
//		if len(buttons) == 0 {
//			msg := tgbotapi.NewMessage(chatID, "Нет доступных очередей.")
//			bot.Send(msg)
//			return
//		}
//
//		msg := tgbotapi.NewMessage(chatID, "Выберите очередь:")
//		msg.ReplyMarkup = tgbotapi.NewReplyKeyboard(buttons...)
//		bot.Send(msg)
//	}
//
// // Обработка выбора очереди
//
//	func handleQueueSelection(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
//		queueName := message.Text
//		var queueID int
//		err := db.QueryRow("SELECT id FROM queues WHERE name = ?", queueName).Scan(&queueID)
//		if err != nil {
//			msg := tgbotapi.NewMessage(message.Chat.ID, "Такой очереди не существует. Попробуйте снова.")
//			bot.Send(msg)
//			return
//		}
//
//		_, err = db.Exec("INSERT INTO queue_entries (queue_id, user_id, username) VALUES (?, ?, ?)",
//			queueID, message.Chat.ID, message.Chat.UserName)
//		if err != nil {
//			log.Printf("Ошибка записи в очередь: %v", err)
//			msg := tgbotapi.NewMessage(message.Chat.ID, "Ошибка при записи в очередь.")
//			bot.Send(msg)
//			return
//		}
//
//		msg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("Вы успешно записались в очередь \"%s\"!", queueName))
//		bot.Send(msg)
//
//		delete(userStates, message.Chat.ID)
//	}
//
// // Обработка показа состава очереди
//
//	func handleQueueShow(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
//		queueName := message.Text
//		var queueID int
//		err := db.QueryRow("SELECT id FROM queues WHERE name = ?", queueName).Scan(&queueID)
//		if err != nil {
//			msg := tgbotapi.NewMessage(message.Chat.ID, "Такой очереди не существует. Попробуйте снова.")
//			bot.Send(msg)
//			return
//		}
//
//		rows, err := db.Query("SELECT username FROM queue_entries WHERE queue_id = ?", queueID)
//		if err != nil {
//			log.Printf("Ошибка при загрузке пользователей очереди: %v", err)
//			msg := tgbotapi.NewMessage(message.Chat.ID, "Ошибка при загрузке пользователей очереди.")
//			bot.Send(msg)
//			return
//		}
//		defer rows.Close()
//
//		var users []string
//		for rows.Next() {
//			var username string
//			err := rows.Scan(&username)
//			if err != nil {
//				log.Printf("Ошибка при обработке пользователей очереди: %v", err)
//				continue
//			}
//			users = append(users, username)
//		}
//
//		if len(users) == 0 {
//			msg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("Очередь \"%s\" пуста.", queueName))
//			bot.Send(msg)
//		} else {
//			msg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("Очередь \"%s\":\n%s", queueName, strings.Join(users, "\n")))
//			bot.Send(msg)
//		}
//
//		delete(userStates, message.Chat.ID) // Сброс состояния
//	}
//
// // Обработка создания очереди
func handleQueueCreation(bot *tgbotapi.BotAPI, message *tgbotapi.Message) {
	queueName := message.Text

	_, err := db.Exec("INSERT INTO queues (name, created_by) VALUES (?, ?)", queueName, message.Chat.ID)
	if err != nil {
		log.Printf("Ошибка создания очереди: %v", err)
		msg := tgbotapi.NewMessage(message.Chat.ID, "Ошибка при создании очереди.")
		bot.Send(msg)
		return
	}

	msg := tgbotapi.NewMessage(message.Chat.ID, fmt.Sprintf("Очередь \"%s\" успешно создана!", queueName))
	msg.ReplyMarkup = mainMenu()
	bot.Send(msg)

	delete(userStates, message.Chat.ID) // Сбрасываем состояние пользователя
}

func handleCallback(bot *tgbotapi.BotAPI, callbackQuery *tgbotapi.CallbackQuery) {
	data := callbackQuery.Data

	if data == "join_queue" {
		// Логика записи в очередь
		msg := tgbotapi.NewMessage(callbackQuery.Message.Chat.ID, "Вы записаны в очередь!")
		bot.Send(msg)
	} else if data == "create_queue" {
		// Логика создания новой очереди
		msg := tgbotapi.NewMessage(callbackQuery.Message.Chat.ID, "Создание новой очереди...")
		bot.Send(msg)
	} else if data == "edit_queue" {
		// Логика редактирования очереди
		msg := tgbotapi.NewMessage(callbackQuery.Message.Chat.ID, "Редактирование очереди...")
		bot.Send(msg)
	}

	// Ответ на CallbackQuery
	callback := tgbotapi.NewCallback(callbackQuery.ID, "Запрос обработан!")
	if _, err := bot.Request(callback); err != nil {
		log.Printf("Ошибка при ответе на CallbackQuery: %v", err)
	}
}
