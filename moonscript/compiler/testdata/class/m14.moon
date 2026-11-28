import Base from require "base"
class Sub extends Base
  new: (...) =>
    super ...
    @x = [super\each! for i=1,2]
  @__inherited: (child) =>
    super child
