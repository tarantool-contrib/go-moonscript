class Outer extends Base
  method: =>
    class Inner extends Other
      method: => super!
    super!
  @cmeth: => super\cmeth!, super.x, super[y]
