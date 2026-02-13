```
sed -i -e 's/HELLO, DUDE/ENJOY YOUR NOODLE SOUP/' vllm/platforms/__init__.py
sed -i -e 's/ENJOY YOUR NOODLE SOUP/HELLO, DUDE/' vllm/platforms/__init__.py
```

k8shazgpu vllm run --name instance-one --follow -- vllm serve facebook/opt-125m --gpu-memory-utilization 0.8