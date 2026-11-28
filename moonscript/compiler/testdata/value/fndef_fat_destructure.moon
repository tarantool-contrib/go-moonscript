class C
  new: ({:a, :b} = {}) =>
    @a = a
  m: (x, {y}, z) => x + y + z
