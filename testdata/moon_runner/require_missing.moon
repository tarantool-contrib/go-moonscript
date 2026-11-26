ok, err = pcall require, "does.not.exist"
print ok
-- the list of searched files depends on package.path/cpath; print the head
print (err\match "^module '[^']*' not found:")
print (err\match "no field package.preload%['does.not.exist'%]")
print (err\match "no file '%./does/not/exist%.lua'")
