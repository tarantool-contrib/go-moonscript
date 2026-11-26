create_person = (name,  relatives) ->
  with Person!
    .name = name
    \add_relative relative for relative in *relatives

me = create_person "Leaf", {dad, mother, sister}
