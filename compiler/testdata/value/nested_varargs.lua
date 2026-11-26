local f
f = function(...)
  local x
  if a then
    do
      local _accum_0 = { }
      local _len_0 = 1
      local _list_0 = {
        ...
      }
      for _index_0 = 1, #_list_0 do
        local v = _list_0[_index_0]
        _accum_0[_len_0] = v
        _len_0 = _len_0 + 1
      end
      x = _accum_0
    end
  end
  return print((function(...)
    return select("#", ...)
  end)(...))
end
