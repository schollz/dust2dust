-- dust2dust
--

dust2dust_=include("dust2dust/lib/dust2dust")
tabutil=require("tabutil")

function init()

  print("initializing dust2dust")
  dust2dust=dust2dust_:new({room="dust"})

  -- receive data using a callback
  dust2dust:receive(function(data)
    -- data is a table
    tabutil.print(data)
  end)

  clock.run(function()
    while true do
      clock.sleep(1)
      -- send a table of data using dust2dust:send(t)
      dust2dust:send({hello="world",1,2,3,tables={are="cool"},beats=clock.get_beats()})
    end
  end)

end

function cleanup()
  dust2dust:stop()
end
