build:
	go build -v

buildarm:
	GOOS=linux GOARCH=arm GOARM=7 go build -v

lua-format.py:
	wget wget https://raw.githubusercontent.com/schollz/LuaFormat/master/lua-format.py

format: lua-format.py
	python3 lua-format.py dust2dust.lua
	python3 lua-format.py lib/dust2dust.lua
