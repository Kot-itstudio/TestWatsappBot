extends Control

var items := ["Играть", "Стоп", "Луп", "Майд", "Звони", "Выйти"]
var index := 0

@onready var list := $VBoxContainer as VBoxContainer


func _ready() -> void:
	_refresh()


func _unhandled_input(event: InputEvent) -> void:
	if event.is_action_pressed("ui_up"):
		_move_up()
	elif event.is_action_pressed("ui_down"):
		_move_down()
	elif event.is_action_pressed("ui_accept"):
		_pick_current()


func _move_up() -> void:
	index = posmod(index - 1, items.size())
	_refresh()


func _move_down() -> void:
	index = posmod(index + 1, items.size())
	_refresh()


func _pick(number: int) -> void:
	if number < 1 or number > items.size():
		return
	index = number - 1
	_refresh()
	_pick_current()


func _pick_current() -> void:
	print("Выбран пункт: ", items[index])


func _refresh() -> void:
	for child in list.get_children():
		list.remove_child(child)
		child.queue_free()
	for i in items.size():
		var label := Label.new()
		var marker := ">" if i == index else " "
		label.text = "%s %d. %s" % [marker, i + 1, items[i]]
		list.add_child(label)
