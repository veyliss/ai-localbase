# 公开科技与 AI 事实夹具

> 版本：public-v1<br>
> 生成日期：2026-08-29<br>
> 用途：公开评测与浏览器级索引流程
> 说明：只保留支撑评测问题所需的最小事实，不包含网页缓存、用户内容或整页复制。

## Qdrant

### Qdrant collection {#qdrant-collection-definition}

在 Qdrant 中，collection 是一组带有向量和 payload 的点的命名集合，可以在其中进行搜索。

来源：<https://qdrant.tech/documentation/overview/what-is-qdrant/index.md>

### Qdrant collection 中的向量约束 {#qdrant-vector-constraints}

同一个 Qdrant collection 中每个点的向量必须具有相同的维度，并使用同一个距离度量进行比较。

来源：<https://qdrant.tech/documentation/manage-data/collections/index.md>

### Qdrant 的常用距离度量 {#qdrant-distance-metrics}

Qdrant 支持点积、余弦相似度、欧氏距离和曼哈顿距离。

来源：<https://qdrant.tech/documentation/manage-data/collections/index.md>

### Qdrant payload {#qdrant-payload}

Qdrant 的 payload 可以存储能够表示为 JSON 的任意信息。

来源：<https://qdrant.tech/documentation/manage-data/payload/index.md>

### Qdrant 混合搜索 {#qdrant-hybrid-search}

在 Qdrant 的文本搜索中，组合 dense 和 sparse 向量可以同时利用语义理解和精确词匹配。

来源：<https://qdrant.tech/documentation/search/hybrid-queries/index.md>

## Hugging Face Transformers

### Transformers 定义 {#transformers-definition}

Hugging Face Transformers 是一个面向文本、计算机视觉、音频、视频和多模态模型的机器学习模型定义框架，可用于推理和训练。

来源：<https://huggingface.co/docs/transformers/index.md>

### Transformers Pipeline {#transformers-pipeline}

Transformers 的 Pipeline 可用于文本生成、图像分割、自动语音识别和文档问答等任务。

来源：<https://huggingface.co/docs/transformers/index.md>

### Transformers Trainer {#transformers-trainer}

Transformers 的 Trainer 支持混合精度、torch.compile 和 FlashAttention 等训练能力。

来源：<https://huggingface.co/docs/transformers/index.md>

### Transformers 模型类 {#transformers-model-classes}

Transformers 中的模型主要由配置、模型和预处理器三个类组成。

来源：<https://huggingface.co/docs/transformers/index.md>

## scikit-learn

### scikit-learn 定义 {#scikit-learn-definition}

scikit-learn 是一个开源机器学习库，支持监督学习和无监督学习。

来源：<https://scikit-learn.org/stable/getting_started.html>

### scikit-learn 工具 {#scikit-learn-tools}

scikit-learn 提供模型拟合、数据预处理、模型选择和模型评估等工具。

来源：<https://scikit-learn.org/stable/getting_started.html>

## PyTorch

### torch.Tensor {#pytorch-tensor}

PyTorch 的 torch.Tensor 是一个包含单一数据类型元素的多维矩阵。

来源：<https://pytorch.org/docs/2.9/tensors.html>

## TensorFlow

### tf.function {#tensorflow-tf-function}

TensorFlow 的 tf.function 可以让程序从即时执行切换到图执行。

来源：<https://www.tensorflow.org/guide/intro_to_graphs>

### TensorFlow 图执行 {#tensorflow-graph-execution}

TensorFlow 官方文档指出，图执行可以脱离 Python 使用，并且通常能够提供更好的性能。

来源：<https://www.tensorflow.org/guide/intro_to_graphs>

## Qdrant 过滤与索引

### Qdrant 过滤子句 {#qdrant-filter-clauses}

Qdrant 可以使用 must、should 和 must_not 等子句组合过滤条件。

来源：<https://qdrant.tech/documentation/search/filtering/index.md>

### Qdrant 过滤条件的作用 {#qdrant-filter-purpose}

当对象的全部特征无法用 embedding 表达时，Qdrant 可以通过 payload 条件补充过滤。

来源：<https://qdrant.tech/documentation/search/filtering/index.md>

### Qdrant payload index {#qdrant-payload-index}

Qdrant 的 payload index 用于加快符合过滤条件的点查询，并帮助查询规划估算过滤结果数量。

来源：<https://qdrant.tech/documentation/manage-data/indexing/index.md>

### Qdrant payload index 的资源代价 {#qdrant-payload-index-cost}

创建 payload index 会额外消耗计算资源和内存，因此应谨慎选择需要索引的字段。

来源：<https://qdrant.tech/documentation/manage-data/indexing/index.md>

## Transformers tokenizer

### Tokenizer 的职责 {#transformers-tokenizer-responsibility}

Tokenizer 负责为模型准备输入，将原始文本切分为 token，并将 token 转换为整数编码。

来源：<https://huggingface.co/docs/transformers/main_classes/tokenizer>

### Fast tokenizer 的性能特点 {#transformers-fast-tokenizer}

Fast tokenizer 基于 Rust 的 Tokenizers 库，并且在批量分词时可以显著加速。

来源：<https://huggingface.co/docs/transformers/main_classes/tokenizer>

### Tokenizer 的字符映射 {#transformers-tokenizer-mapping}

Fast tokenizer 可以在原始字符位置和 token 空间之间进行映射。

来源：<https://huggingface.co/docs/transformers/main_classes/tokenizer>

## scikit-learn 数据预处理

### train_test_split 的用途 {#sklearn-train-test-split}

train_test_split 用于将数组或矩阵随机拆分为训练子集和测试子集。

来源：<https://scikit-learn.org/stable/modules/generated/sklearn.model_selection.train_test_split.html>

### train_test_split 的 random_state {#sklearn-random-state}

train_test_split 的 random_state 控制拆分前的数据打乱；传入整数可以让多次调用产生可复现的结果。

来源：<https://scikit-learn.org/stable/modules/generated/sklearn.model_selection.train_test_split.html>

### StandardScaler 的标准化方式 {#sklearn-standard-scaler}

StandardScaler 通过移除均值并缩放到单位方差来标准化特征。

来源：<https://scikit-learn.org/stable/modules/generated/sklearn.preprocessing.StandardScaler.html>

### StandardScaler 的训练统计量 {#sklearn-standard-scaler-statistics}

StandardScaler 会保存训练样本的均值和标准差，用于后续数据变换。

来源：<https://scikit-learn.org/stable/modules/generated/sklearn.preprocessing.StandardScaler.html>

## PyTorch 自动微分

### torch.autograd 的作用 {#pytorch-autograd}

PyTorch 的 torch.autograd 可以自动计算梯度。

来源：<https://pytorch.org/tutorials/beginner/basics/autogradqs_tutorial.html>

### requires_grad 与梯度跟踪 {#pytorch-requires-grad}

设置 requires_grad=True 的 Tensor 会被 autograd 跟踪其运算，以便进行梯度计算。

来源：<https://pytorch.org/tutorials/beginner/basics/autogradqs_tutorial.html>

### PyTorch 反向传播 {#pytorch-backward}

在 PyTorch 中可以调用 loss.backward() 计算梯度。

来源：<https://pytorch.org/tutorials/beginner/basics/autogradqs_tutorial.html>

## TensorFlow Keras

### Sequential 模型的结构 {#tensorflow-sequential-structure}

TensorFlow 的 Sequential 模型适合按顺序堆叠多个层、且每层只有一个输入张量和一个输出张量的结构。

来源：<https://www.tensorflow.org/guide/keras/sequential_model>

### Sequential 模型的训练配置 {#tensorflow-sequential-training}

Keras Sequential 模型可以通过 compile 配置训练，再使用 fit 进行训练。

来源：<https://www.tensorflow.org/guide/keras/sequential_model>
