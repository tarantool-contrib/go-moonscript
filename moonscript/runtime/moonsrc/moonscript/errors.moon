
-- Port note: upstream moonscript/errors.moon v0.8.0 with two changes for
-- gopher-lua:
-- * the lpeg grammar in rewrite_traceback (lpeg isn't available) is
--   replaced by parse_traceback, which accepts exactly the same input
--   (P{Header: "stack traceback:" * "\n" * Ct(Line^1),
--      Line: "\t" * C((1 - "\n")^0) * ("\n" + -1)});
-- * reverse_line_number converts line_num with tonumber: rewrite_traceback
--   passes the line as a string, and gopher-lua's numeric for, unlike PUC
--   Lua's, doesn't coerce strings.
util = require "moonscript.util"

import concat, insert from table
import split, pos_to_line from util

user_error = (...) ->
  error {"user-error", ...}

-- find the line number of `pos` chars into fname
lookup_line = (fname, pos, cache) ->
  if not cache[fname]
    with assert io.open(fname)
      cache[fname] = \read "*a"
      \close!
  pos_to_line cache[fname], pos

-- reverse the line number in fname using line_table
reverse_line_number = (fname, line_table, line_num, cache) ->
  for i = tonumber(line_num),0,-1
    if line_table[i]
      return lookup_line fname, line_table[i], cache
  "unknown"


-- strip traceback lines up to chunk func
-- replace reference to chunk_func with "main chunk"
truncate_traceback = (traceback, chunk_func="moonscript_chunk") ->
  traceback = split traceback, "\n"
  stop = #traceback
  while stop > 1
    break if traceback[stop]\match chunk_func
    stop -= 1

  traceback = [t for t in *traceback[1,stop]]

  rep = "function '" .. chunk_func .. "'"
  traceback[#traceback] = traceback[#traceback]\gsub rep, "main chunk"

  concat traceback, "\n"

rewrite_traceback = (text, err) ->
  line_tables = require "moonscript.line_tables"
  header_text = "stack traceback:"

  -- the lines following the header, each without its leading tab; nil
  -- unless text starts with the header and at least one tab line
  parse_traceback = (text) ->
    prefix = header_text .. "\n"
    return nil unless text\sub(1, #prefix) == prefix
    lines = {}
    pos = #prefix + 1
    while text\sub(pos, pos) == "\t"
      nl = text\find "\n", pos, true
      if nl
        insert lines, text\sub pos + 1, nl - 1
        pos = nl + 1
      else
        insert lines, text\sub pos + 1
        break
    return nil if #lines == 0
    lines

  cache = {} -- loaded file cache
  rewrite_single = (trace) ->
    fname, line, msg = trace\match '^(.-):(%d+): (.*)$'
    tbl = line_tables["@#{fname}"]
    if fname and tbl
      concat {
        fname, ":"
        reverse_line_number(fname, tbl, line, cache)
        ": "
        "(", line, ") "
        msg
      }
    else
      trace

  err = rewrite_single err
  match = parse_traceback text

  return nil unless match

  for i, trace in ipairs match
    match[i] = rewrite_single trace

  concat {
    "moon: " .. err
    header_text
    "\t" .. concat match, "\n\t"
  }, "\n"


{ :rewrite_traceback, :truncate_traceback, :user_error, :reverse_line_number }

