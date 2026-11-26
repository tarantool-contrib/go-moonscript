local some_instance_method
some_instance_method = function(self, ...)
  return self.__class(...)
end