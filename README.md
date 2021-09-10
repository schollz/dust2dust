# dust2dust

send and receive data from other norns.

## api

```lua
dust2dust_=include("dust2dust/lib/dust2dust")
dust2dust=dust2dust_:new({room="dust"})

-- now you can send data to everyone in room "dust"
dust2dust:send({hello="world",1,2,3,tables={are="cool"}})

-- or receive data with a callback:
dust2dust:receive(function(data)
    print("received data:")
    tab.print(data)
end)

-- make sure to cleanup to stop the local server
function cleanup()
  dust2dust:stop()
end
```

## how it works

a local server runs when you start `dust2dust`. this server listens to osc from norns when you send data and transmits it to a relay server which relays it. the local server listens to this relay server and transmits incoming messages via osc to the local norns. 

## license

MIT